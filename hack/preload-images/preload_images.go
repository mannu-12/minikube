/*
Copyright 2020 The Kubernetes Authors All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/minikube/pkg/drivers/kic"
	"k8s.io/minikube/pkg/drivers/kic/oci"
	"k8s.io/minikube/pkg/minikube/bootstrapper/images"
	"k8s.io/minikube/pkg/minikube/driver"
	"k8s.io/minikube/pkg/minikube/localpath"
)

const (
	profile      = "generate-preloaded-images-tar"
	minikubePath = "out/minikube"
)

var (
	kubernetesVersion       = ""
	tarballFilename         = ""
	dockerStorageDriver     = ""
	preloadedTarballVersion = ""
	containerRuntime        = ""
)

func init() {
	flag.StringVar(&kubernetesVersion, "kubernetes-version", "", "desired kubernetes version, for example `v1.17.2`")
	flag.StringVar(&dockerStorageDriver, "docker-storage-driver", "overlay2", "docker storage driver backend")
	flag.StringVar(&preloadedTarballVersion, "preloaded-tarball-version", "", "preloaded tarball version")
	flag.StringVar(&containerRuntime, "container-runtime", "docker", "container runtime")

	flag.Parse()
	tarballFilename = fmt.Sprintf("preloaded-images-k8s-%s-%s-%s-%s.tar.lz4", preloadedTarballVersion, kubernetesVersion, containerRuntime, dockerStorageDriver)
}

func main() {
	if err := verifyDockerStorage(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := executePreloadImages(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func executePreloadImages() error {
	defer func() {
		if err := deleteMinikube(); err != nil {
			fmt.Println(err)
		}
	}()

	driver := kic.NewDriver(kic.Config{
		KubernetesVersion: kubernetesVersion,
		ContainerRuntime:  driver.Docker,
		OCIBinary:         oci.Docker,
		MachineName:       profile,
		ImageDigest:       kic.BaseImage,
		StorePath:         localpath.MiniPath(),
		CPU:               2,
		Memory:            4000,
		APIServerPort:     8080,
	})

	baseDir := filepath.Dir(driver.GetSSHKeyPath())
	defer os.Remove(baseDir)

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return err
	}
	if err := driver.Create(); err != nil {
		return errors.Wrap(err, "creating kic driver")
	}

	// Now, get images to pull
	imgs, err := images.Kubeadm("", kubernetesVersion)
	if err != nil {
		return errors.Wrap(err, "kubeadm images")
	}

	for _, img := range append(imgs, kic.OverlayImage) {
		cmd := exec.Command("docker", "exec", profile, "docker", "pull", img)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return errors.Wrapf(err, "downloading %s", img)
		}
	}

	// Create image tarball
	if err := createImageTarball(); err != nil {
		return err
	}
	return copyTarballToHost()
}

func createImageTarball() error {
	dirs := []string{
		fmt.Sprintf("./lib/docker/%s", dockerStorageDriver),
		"./lib/docker/image",
	}
	args := []string{"exec", profile, "sudo", "tar", "-I", "lz4", "-C", "/var", "-cvf", tarballFilename}
	args = append(args, dirs...)
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "creating image tarball")
	}
	return nil
}

func copyTarballToHost() error {
	dest := filepath.Join("out/", tarballFilename)
	cmd := exec.Command("docker", "cp", fmt.Sprintf("%s:/%s", profile, tarballFilename), dest)
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "copying tarball to host")
	}
	return nil
}

func deleteMinikube() error {
	cmd := exec.Command(minikubePath, "delete", "-p", profile)
	cmd.Stdout = os.Stdout
	return cmd.Run()
}

func verifyDockerStorage() error {
	cmd := exec.Command("docker", "info", "-f", "{{.Info.Driver}}")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	driver := strings.Trim(string(output), " \n")
	if driver != dockerStorageDriver {
		return fmt.Errorf("docker storage driver %s does not match requested %s", driver, dockerStorageDriver)
	}
	return nil
}