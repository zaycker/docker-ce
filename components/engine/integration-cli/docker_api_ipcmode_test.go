// build +linux
package main

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/integration-cli/checker"
	"github.com/docker/docker/integration-cli/cli"
	"github.com/go-check/check"
)

/* testIpcCheckDevExists checks whether a given mount (identified by its
 * major:minor pair from /proc/self/mountinfo) exists on the host system.
 *
 * The format of /proc/self/mountinfo is like:
 *
 * 29 23 0:24 / /dev/shm rw,nosuid,nodev shared:4 - tmpfs tmpfs rw
 *       ^^^^\
 *            - this is the minor:major we look for
 */
func testIpcCheckDevExists(mm string) (bool, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 7 {
			continue
		}
		if fields[2] == mm {
			return true, nil
		}
	}

	return false, s.Err()
}

// testIpcContainer is a helper function to test --ipc container:NNN mode in various scenarios
func testIpcContainer(s *DockerSuite, c *check.C, donorMode string, mustWork bool) {
	cfg := container.Config{
		Image: "busybox",
		Cmd:   []string{"top"},
	}
	hostCfg := container.HostConfig{
		IpcMode: container.IpcMode(donorMode),
	}
	ctx := context.Background()

	client := testEnv.APIClient()

	// create and start the "donor" container
	resp, err := client.ContainerCreate(ctx, &cfg, &hostCfg, nil, "")
	c.Assert(err, checker.IsNil)
	c.Assert(len(resp.Warnings), checker.Equals, 0)
	name1 := resp.ID

	err = client.ContainerStart(ctx, name1, types.ContainerStartOptions{})
	c.Assert(err, checker.IsNil)

	// create and start the second container
	hostCfg.IpcMode = container.IpcMode("container:" + name1)
	resp, err = client.ContainerCreate(ctx, &cfg, &hostCfg, nil, "")
	c.Assert(err, checker.IsNil)
	c.Assert(len(resp.Warnings), checker.Equals, 0)
	name2 := resp.ID

	err = client.ContainerStart(ctx, name2, types.ContainerStartOptions{})
	if !mustWork {
		// start should fail with a specific error
		c.Assert(err, checker.NotNil)
		c.Assert(fmt.Sprintf("%v", err), checker.Contains, "non-shareable IPC")
		// no more checks to perform here
		return
	}

	// start should succeed
	c.Assert(err, checker.IsNil)

	// check that IPC is shared
	// 1. create a file in the first container
	cli.DockerCmd(c, "exec", name1, "sh", "-c", "printf covfefe > /dev/shm/bar")
	// 2. check it's the same file in the second one
	out := cli.DockerCmd(c, "exec", "-i", name2, "cat", "/dev/shm/bar").Combined()
	c.Assert(out, checker.Matches, "^covfefe$")
}

/* TestAPIIpcModeShareableAndContainer checks that a container created with
 * --ipc container:ID can use IPC of another shareable container.
 */
func (s *DockerSuite) TestAPIIpcModeShareableAndContainer(c *check.C) {
	testRequires(c, DaemonIsLinux)
	testIpcContainer(s, c, "shareable", true)
}

/* TestAPIIpcModePrivateAndContainer checks that a container created with
 * --ipc container:ID can NOT use IPC of another private container.
 */
func (s *DockerSuite) TestAPIIpcModePrivateAndContainer(c *check.C) {
	testRequires(c, DaemonIsLinux, MinimumAPIVersion("1.32"))
	testIpcContainer(s, c, "private", false)
}

/* TestAPIIpcModeHost checks that a container created with --ipc host
 * can use IPC of the host system.
 */
func (s *DockerSuite) TestAPIIpcModeHost(c *check.C) {
	testRequires(c, DaemonIsLinux, SameHostDaemon, NotUserNamespace)

	cfg := container.Config{
		Image: "busybox",
		Cmd:   []string{"top"},
	}
	hostCfg := container.HostConfig{
		IpcMode: container.IpcMode("host"),
	}
	ctx := context.Background()

	client := testEnv.APIClient()
	resp, err := client.ContainerCreate(ctx, &cfg, &hostCfg, nil, "")
	c.Assert(err, checker.IsNil)
	c.Assert(len(resp.Warnings), checker.Equals, 0)
	name := resp.ID

	err = client.ContainerStart(ctx, name, types.ContainerStartOptions{})
	c.Assert(err, checker.IsNil)

	// check that IPC is shared
	// 1. create a file inside container
	cli.DockerCmd(c, "exec", name, "sh", "-c", "printf covfefe > /dev/shm/."+name)
	// 2. check it's the same on the host
	bytes, err := ioutil.ReadFile("/dev/shm/." + name)
	c.Assert(err, checker.IsNil)
	c.Assert(string(bytes), checker.Matches, "^covfefe$")
	// 3. clean up
	cli.DockerCmd(c, "exec", name, "rm", "-f", "/dev/shm/."+name)
}
