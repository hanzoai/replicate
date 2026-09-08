//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Self-hosted S3 server used by the docker-backed integration tests.
const (
	s3TestImage     = "ghcr.io/hanzoai/s3:v1.0.14"
	awsCLIImage     = "amazon/aws-cli"
	s3TestAccessKey = "hanzo"
	s3TestSecretKey = "hanzos3secret"

	// s3TestDomain is the host suffix the server treats as virtual-host style,
	// i.e. requests arrive as <bucket>.<s3TestDomain>. Enabled with the
	// -s3.domainName flag (the S3 server's equivalent of a vhost domain list).
	s3TestDomain = "s3-accesspoint.127.0.0.1.nip.io"
)

func RequireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("Docker is not available, skipping test")
	}
}

// awsCLI builds a docker-run of the AWS CLI linked to the given S3 container.
func awsCLI(containerID string, args ...string) *exec.Cmd {
	const alias = "s3"
	argv := []string{"run", "--rm",
		"--link", containerID + ":" + alias,
		"-e", "AWS_ACCESS_KEY_ID=" + s3TestAccessKey,
		"-e", "AWS_SECRET_ACCESS_KEY=" + s3TestSecretKey,
		"-e", "AWS_DEFAULT_REGION=us-east-1",
		awsCLIImage, "--endpoint-url", "http://" + alias + ":9000",
	}
	return exec.Command("docker", append(argv, args...)...)
}

// waitForS3Ready polls the S3 API health endpoint until it responds.
func waitForS3Ready(endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := http.Get(endpoint + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("S3 endpoint %s not ready after %s", endpoint, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func StartS3TestContainer(t *testing.T) (string, string) {
	t.Helper()

	name := fmt.Sprintf("replicate-s3-%d", time.Now().UnixNano())
	exec.Command("docker", "rm", "-f", name).Run()

	// The image's default command serves the S3 API on :9000 from /data. It is
	// overridden here only to add -s3.domainName, which turns on virtual-host
	// style bucket addressing (<bucket>.<domain>) that the access-point test
	// relies on.
	args := []string{
		"run", "-d",
		"--name", name,
		"-p", "0:9000",
		"-e", "AWS_ACCESS_KEY_ID=" + s3TestAccessKey,
		"-e", "AWS_SECRET_ACCESS_KEY=" + s3TestSecretKey,
		s3TestImage,
		"server", "-s3", "-s3.port=9000", "-dir=/data", "-s3.domainName=" + s3TestDomain,
	}
	containerID := runDockerCommand(t, args...)
	portInfo := runDockerCommand(t, "port", name, "9000/tcp")
	hostPort := parseDockerPort(t, portInfo)

	endpoint := fmt.Sprintf("http://localhost:%s", hostPort)
	if err := waitForS3Ready(endpoint, 60*time.Second); err != nil {
		t.Fatalf("S3 container %s not ready: %v", name, err)
	}

	t.Logf("Started S3 container %s (%s) on port %s", name, containerID[:12], hostPort)
	return name, endpoint
}

func StopS3TestContainer(t *testing.T, name string) {
	t.Helper()
	if name == "" {
		return
	}
	if os.Getenv("SOAK_KEEP_TEMP") != "" {
		t.Logf("SOAK_KEEP_TEMP set, preserving S3 container: %s", name)
		return
	}
	exec.Command("docker", "rm", "-f", name).Run()
}

func runDockerCommand(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s failed: %v\nOutput: %s", strings.Join(args, " "), err, string(output))
	}
	return strings.TrimSpace(string(output))
}

func parseDockerPort(t *testing.T, portInfo string) string {
	t.Helper()
	idx := strings.LastIndex(portInfo, ":")
	if idx == -1 || idx == len(portInfo)-1 {
		t.Fatalf("unexpected docker port output: %s", portInfo)
	}
	return portInfo[idx+1:]
}
