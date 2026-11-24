// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A deployment can answer its create before it is ready. These run the generated
// `deployments create` the way the binary does and check that it waits, what it reads, what it
// prints and how it exits.

// deploymentAPI answers the create with one deployment, then each read with the next one in
// turn, repeating the last. It keeps a line per request.
type deploymentAPI struct {
	created string
	reads   []string

	mu       sync.Mutex
	requests []string
}

func (a *deploymentAPI) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.requests = append(a.requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, a.created)
			return
		}
		if len(a.reads) == 0 {
			t.Errorf("unexpected read: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		read := a.reads[0]
		if len(a.reads) > 1 {
			a.reads = a.reads[1:]
		}
		_, _ = io.WriteString(w, read)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (a *deploymentAPI) log() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.Join(a.requests, "\n")
}

const waitingDeploymentID = "3f6c1e7a-2b4d-4c8e-9a1f-5d7e9b0c2a4f"

// deploymentBody is a deployment as the API returns it, in the given status.
func deploymentBody(status, detail string) string {
	statusDetail := "null"
	if detail != "" {
		statusDetail = fmt.Sprintf("%q", detail)
	}
	return fmt.Sprintf(`{"id": %q, "name": "traces", "type": "AGENT_OBSERVABILITY",
		"runtime_platform": null, "enabled": false,
		"created_time": "2026-11-23T15:00:00Z", "last_updated_time": "2026-11-23T15:00:00Z",
		"status": %q, "status_detail": %s, "deletion_protection": true, "aws_external_id": null}`,
		waitingDeploymentID, status, statusDetail)
}

// createDeployment runs `deployments create` against api and prints in the given output
// format. The wait polls every millisecond, and one second of its timeout lasts a millisecond.
func createDeployment(t *testing.T, api *deploymentAPI, output string, extra ...string) (code int, stdout, stderr string) {
	t.Helper()
	return createDeploymentWithin(t, api, time.Millisecond, output, extra...)
}

// createDeploymentWithin is createDeployment with timeoutUnit as the length of one second of
// the wait's timeout.
func createDeploymentWithin(t *testing.T, api *deploymentAPI, timeoutUnit time.Duration, output string, extra ...string) (code int, stdout, stderr string) {
	t.Helper()
	prev := []time.Duration{statusPollInterval, statusTimeoutUnit}
	statusPollInterval, statusTimeoutUnit = time.Millisecond, timeoutUnit
	t.Cleanup(func() { statusPollInterval, statusTimeoutUnit = prev[0], prev[1] })
	args := append([]string{
		"deployments", "create", "--type", "AGENT_OBSERVABILITY",
		"--endpoint", api.serve(t).URL,
		"--api-id", "i", "--api-token", "s",
		"--config-dir", t.TempDir(),
		"--output", output,
	}, extra...)
	return runExit(t, context.Background(), args...)
}

// printedStatus is the status of the deployment the command printed on stdout.
func printedStatus(t *testing.T, stdout string) string {
	t.Helper()
	var printed struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &printed); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	return printed.Status
}

func TestDeploymentsCreateReadsNothingWhenTheDeploymentIsAlreadyReady(t *testing.T) {
	api := &deploymentAPI{created: deploymentBody("READY", "")}

	code, stdout, stderr := createDeployment(t, api, "json")

	if code != exitOK {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if got := api.log(); got != "POST /api/v2/deployments" {
		t.Errorf("requests:\n%s\nwant the create alone", got)
	}
	if stderr != "" {
		t.Errorf("stderr %q, want nothing", stderr)
	}
	if got := printedStatus(t, stdout); got != "READY" {
		t.Errorf("printed status %s, want READY", got)
	}
}

func TestDeploymentsCreateWaitsUntilTheDeploymentIsReady(t *testing.T) {
	api := &deploymentAPI{
		created: deploymentBody("PROVISIONING", ""),
		reads: []string{
			deploymentBody("PROVISIONING", ""),
			deploymentBody("READY", ""),
		},
	}

	code, stdout, stderr := createDeployment(t, api, "json")

	if code != exitOK {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	read := "GET /api/v2/deployments/" + waitingDeploymentID
	if got, want := api.log(), "POST /api/v2/deployments\n"+read+"\n"+read; got != want {
		t.Errorf("requests:\n%s\nwant:\n%s", got, want)
	}
	if got := printedStatus(t, stdout); got != "READY" {
		t.Errorf("printed status %s, want READY", got)
	}
	if !strings.Contains(stderr, "Waiting for "+waitingDeploymentID+" to be READY") {
		t.Errorf("stderr does not say what it waits for:\n%s", stderr)
	}
}

func TestDeploymentsCreateNoWaitPrintsTheDeploymentAsCreated(t *testing.T) {
	api := &deploymentAPI{created: deploymentBody("PROVISIONING", "")}

	code, stdout, stderr := createDeployment(t, api, "json", "--no-wait")

	if code != exitOK {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if got := api.log(); got != "POST /api/v2/deployments" {
		t.Errorf("requests:\n%s\nwant the create alone", got)
	}
	if got := printedStatus(t, stdout); got != "PROVISIONING" {
		t.Errorf("printed status %s, want PROVISIONING", got)
	}
	if stderr != "" {
		t.Errorf("stderr %q, want nothing", stderr)
	}
}

func TestDeploymentsCreateExitsOneWhenTheDeploymentFails(t *testing.T) {
	const detail = "Setting up the deployment did not finish."
	api := &deploymentAPI{
		created: deploymentBody("PROVISIONING", ""),
		reads: []string{
			deploymentBody("PROVISIONING", ""),
			deploymentBody("FAILED", detail),
		},
	}

	code, stdout, stderr := createDeployment(t, api, "json")

	if code != exitFailure {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitFailure, stderr)
	}
	if got := printedStatus(t, stdout); got != "FAILED" {
		t.Errorf("printed status %s, want FAILED", got)
	}
	for _, want := range []string{
		waitingDeploymentID + " is FAILED: " + detail,
		"check it with: " + binaryName + " deployments get " + waitingDeploymentID,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestDeploymentsCreateExitsOneWhenTheDeploymentIsCreatedFailed(t *testing.T) {
	const detail = "Setting up the deployment did not finish."
	api := &deploymentAPI{created: deploymentBody("FAILED", detail)}

	code, stdout, stderr := createDeployment(t, api, "json")

	if code != exitFailure {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitFailure, stderr)
	}
	if got := api.log(); got != "POST /api/v2/deployments" {
		t.Errorf("requests:\n%s\nwant the create alone", got)
	}
	if got := printedStatus(t, stdout); got != "FAILED" {
		t.Errorf("printed status %s, want FAILED", got)
	}
	if want := waitingDeploymentID + " is FAILED: " + detail; !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
}

func TestDeploymentsCreatePrintsNoTableWhenTheDeploymentFails(t *testing.T) {
	const detail = "Setting up the deployment did not finish."
	api := &deploymentAPI{
		created: deploymentBody("PROVISIONING", ""),
		reads:   []string{deploymentBody("FAILED", detail)},
	}

	code, stdout, stderr := createDeployment(t, api, "table")

	if code != exitFailure {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitFailure, stderr)
	}
	read := "GET /api/v2/deployments/" + waitingDeploymentID
	if got, want := api.log(), "POST /api/v2/deployments\n"+read; got != want {
		t.Errorf("requests:\n%s\nwant:\n%s", got, want)
	}
	if want := waitingDeploymentID + " is FAILED: " + detail; !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout %q, want nothing: the error names the deployment", stdout)
	}
}

func TestDeploymentsCreatePrintsATableWhenTheDeploymentIsReady(t *testing.T) {
	api := &deploymentAPI{
		created: deploymentBody("PROVISIONING", ""),
		reads:   []string{deploymentBody("READY", "")},
	}

	code, stdout, stderr := createDeployment(t, api, "table")

	if code != exitOK {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	for _, want := range []string{waitingDeploymentID, "READY"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the table lacks %q:\n%s", want, stdout)
		}
	}
}

func TestDeploymentsCreateExitsOneWhenTheWaitTimesOut(t *testing.T) {
	api := &deploymentAPI{
		created: deploymentBody("PROVISIONING", ""),
		reads:   []string{deploymentBody("PROVISIONING", "")},
	}

	// The create's timeout is 1800 seconds, so this one lasts 18ms.
	code, stdout, stderr := createDeploymentWithin(t, api, 10*time.Microsecond, "json")

	if code != exitFailure {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, exitFailure, stderr)
	}
	if got := printedStatus(t, stdout); got != "PROVISIONING" {
		t.Errorf("printed status %s, want PROVISIONING", got)
	}
	for _, want := range []string{
		"stopped waiting after 30m0s; " + waitingDeploymentID + " is still PROVISIONING",
		"check it with: " + binaryName + " deployments get " + waitingDeploymentID,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}
