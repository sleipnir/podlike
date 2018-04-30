package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/mattn/go-shellwords"
	"github.com/rycus86/podlike/config"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"time"
)

func (c *Component) createContainer(configuration *config.Configuration) (string, error) {
	entrypoint, err := asStrSlice(c.Entrypoint)
	if err != nil {
		return "", nil
	}

	command, err := asStrSlice(c.Command)
	if err != nil {
		return "", err
	}

	if configuration.AlwaysPull {
		if err := c.pullImage(); err != nil {
			return "", err
		}
	}

	name := c.client.container.Name + ".podlike." + c.Name

	containerConfig := container.Config{
		Image:      c.Image,
		Entrypoint: entrypoint,
		Cmd:        command,
		WorkingDir: c.WorkingDir,
		Env:        c.Environment,
		Labels:     c.Labels,
		Tty:        c.Tty,
		StopSignal: c.StopSignal,
	}

	if c.StopGracePeriod.Seconds() > 0 {
		stopTimeoutSeconds := int(c.StopGracePeriod.Seconds())
		containerConfig.StopTimeout = &stopTimeoutSeconds
	}

	hostConfig := container.HostConfig{
		AutoRemove: true,

		Resources: container.Resources{
			CgroupParent: c.client.cgroup,
		},

		Cgroup:      container.CgroupSpec("container:" + c.client.container.ID),
		IpcMode:     container.IpcMode("container:" + c.client.container.ID),
		NetworkMode: container.NetworkMode("container:" + c.client.container.ID),
	}

	if configuration.SharePids {
		hostConfig.PidMode = container.PidMode("container:" + c.client.container.ID)
	}

	if configuration.ShareVolumes {
		hostConfig.VolumesFrom = []string{c.client.container.ID}
	}

	ctxCreate, cancelCreate := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelCreate()

	created, err := c.client.api.ContainerCreate(ctxCreate,
		&containerConfig,
		&hostConfig,
		&network.NetworkingConfig{},
		name)

	if err != nil {
		if client.IsErrNotFound(err) {
			if err := c.pullImage(); err != nil {
				return "", err
			}

			created, err = c.client.api.ContainerCreate(ctxCreate,
				&containerConfig,
				&hostConfig,
				&network.NetworkingConfig{},
				name)

			if err != nil {
				return "", err
			} else {
				return created.ID, nil
			}
		} else {
			return "", err
		}
	} else {
		for _, warning := range created.Warnings {
			fmt.Sprintf("[%s] Warning: %s\n", c.Name, warning)
		}

		return created.ID, nil
	}
}

func asStrSlice(value interface{}) (strslice.StrSlice, error) {
	if value == nil {
		return nil, nil
	}

	stringValue, ok := value.(string)
	if ok {
		return shellwords.Parse(stringValue)
	}

	sliceValue, ok := value.([]string)
	if ok {
		return sliceValue, nil
	} else {
		return nil, errors.New(fmt.Sprintf("invalid string or slice: %T %+v", value, value))
	}
}

func (c *Component) pullImage() error {
	fmt.Println("Pulling image:", c.Image)

	// TODO is context.Background() appropriate here?
	if reader, err := c.client.api.ImagePull(context.Background(), c.Image, types.ImagePullOptions{}); err != nil {
		return err
	} else {
		defer reader.Close()

		ioutil.ReadAll(reader)

		return nil
	}
}

func (c *Component) copyFilesIfNecessary() error {
	for key, value := range c.client.container.Config.Labels {
		if strings.Index(key, "pod.copy.") >= 0 {
			if target := strings.TrimPrefix(key, "pod.copy."); target != c.Name {
				continue
			}

			parts := strings.Split(value, ":")
			if len(parts) != 2 {
				return errors.New(fmt.Sprintf("invalid pod.copy configuration: %s", value))
			}

			source := parts[0]
			target := parts[1]

			targetDir, targetFilename := path.Split(target)
			reader, err := createTar(source, targetFilename)
			if err != nil {
				return err
			}

			fmt.Println("Copying", source, "to", c.Name, "@", target, "...")

			return c.client.api.CopyToContainer(
				context.TODO(), c.containerID, targetDir, reader, types.CopyToContainerOptions{})
		}
	}

	return nil
}

func createTar(path, filename string) (io.Reader, error) {
	var b bytes.Buffer

	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	contents, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	tw := tar.NewWriter(&b)
	hdr := tar.Header{
		Name: filename,
		Mode: 0644,
		Size: fi.Size(),
	}
	if err := tw.WriteHeader(&hdr); err != nil {
		return nil, err
	}

	if _, err = tw.Write(contents); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}

	return &b, nil
}