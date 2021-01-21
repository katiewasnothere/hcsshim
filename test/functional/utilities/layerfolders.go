package testutilities

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/containerd/mount"
)

var imageLayers map[string][]string

const defaultPlatform = "windows"

func init() {
	imageLayers = make(map[string][]string)
}

func LayerFolders(t *testing.T, imageName string) []string {
	return LayerFoldersPlatform(t, imageName, defaultPlatform)
}

func LayerFoldersPlatform(t *testing.T, imageName, platform string) []string {
	if _, ok := imageLayers[imageName]; !ok {
		imageLayers[imageName] = getLayers(t, imageName, platform)
	}
	return imageLayers[imageName]
}

/*func getLayers(t *testing.T, imageName string) []string {
	cmd := exec.Command("docker", "inspect", imageName, "-f", `"{{.GraphDriver.Data.dir}}"`)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Skipf("Failed to find layers for %q. Check docker images", imageName)
	}
	imagePath := strings.Replace(strings.TrimSpace(out.String()), `"`, ``, -1)
	layers := getLayerChain(t, imagePath)
	return append([]string{imagePath}, layers...)
}

func getLayerChain(t *testing.T, layerFolder string) []string {
	jPath := filepath.Join(layerFolder, "layerchain.json")
	content, err := ioutil.ReadFile(jPath)
	if os.IsNotExist(err) {
		t.Fatalf("layerchain not found")
	} else if err != nil {
		t.Fatalf("failed to read layerchain")
	}

	var layerChain []string
	err = json.Unmarshal(content, &layerChain)
	if err != nil {
		t.Fatalf("failed to unmarshal layerchain")
	}
	return layerChain
}*/

func getCtrPath() string {
	return filepath.Join(filepath.Dir(os.Args[0]), "ctr.exe")
}

func getSnapshotterName(platform string) string {
	if platform == "windows" {
		return "windows"
	}
	return "windows-lcow"
}

func getLayers(t *testing.T, imageName, platform string) []string {
	cmd := exec.Command(getCtrPath(),
		"--address",
		daemonAddress,
		"--namespace",
		daemonNamespace,
		"images",
		"mounts",
		"--snapshotter",
		getSnapshotterName(platform),
		"--platform",
		platform,
		imageName)

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Skipf("Failed to find layers for %q with %v. Command was %v", imageName, err, cmd)
	}
	mounts := []mount.Mount{}
	if err := json.Unmarshal(out.Bytes(), &mounts); err != nil {
		t.Skipf("Failed to parse layers for %q", imageName)
	}

	if len(mounts) != 1 {
		t.Skip("Rootfs does not contain exactly 1 mount for the root file system")
	}

	// setup layer folders
	m := mounts[0]
	var layerFolders []string
	for _, option := range m.Options {
		if strings.HasPrefix(option, mount.ParentLayerPathsFlag) {
			err := json.Unmarshal([]byte(option[len(mount.ParentLayerPathsFlag):]), &layerFolders)
			if err != nil {
				t.Skipf("failed to unmarshal parent layer paths from mount: %v", err)
			}
		}
	}
	layerFolders = append(layerFolders, m.Source)
	return layerFolders
}
