package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeConnectionsAPI answers the creates and deletes connections add sends, failing the
// requests named in fail with their status, and records every request in order.
type fakeConnectionsAPI struct {
	t    *testing.T
	fail map[string]int
	// Whether the validation run the fake reports passes.
	refuse bool
	// Whether the connection list reports a connection left in the warehouse.
	occupied bool
	mu       sync.Mutex
	calls    []string
	bodies   map[string]map[string]any
	queries  map[string]string
}

var createdBodies = map[string]string{
	"POST /api/v2/warehouses":                  `{"id":"wh-1","name":"n","type":"snowflake","deployment_id":"d","created_time":"2026-09-23T00:00:00Z"}`,
	"POST /api/v2/credentials/snowflake":       `{"id":"cr-1","connection_type":"snowflake","storage_type":"mc_managed","created_time":"2026-09-23T00:00:00Z","account":"a","user":"u","warehouse":null}`,
	"POST /api/v2/credentials/self-hosted/aws": `{"id":"cr-2","connection_type":"bigquery","storage_type":"aws_secrets_manager","created_time":"2026-09-23T00:00:00Z","bq_project_id":"p","databricks_warehouse_id":null,"aws_secret":"s","aws_region":null,"assumable_role":null,"external_id":null}`,
	"POST /api/v2/connections":                 `{"id":"cn-1","connection_type":"snowflake","name":"n","warehouse_id":"wh-1","warehouse_name":"n","deployment_id":"d","deployment_name":"d","credentials_id":"cr-1","credentials_storage_type":"mc_managed","job_types":[],"created_time":"2026-09-23T00:00:00Z"}`,
}

const connectionOut = `"connection_type":"snowflake","name":"n","warehouse_id":"wh-1","warehouse_name":"n",` +
	`"deployment_id":"d","deployment_name":"d","credentials_storage_type":"mc_managed","job_types":[],"created_time":"2026-09-23T00:00:00Z"`

// readBodies answers the reads and updates connections update sends.
var readBodies = map[string]string{
	"GET /api/v2/connections/cn-1":                   `{"id":"cn-1","credentials_id":"cr-1",` + connectionOut + `}`,
	"GET /api/v2/connections/cn-2":                   `{"id":"cn-2","credentials_id":"cr-2",` + strings.Replace(strings.Replace(connectionOut, `"snowflake"`, `"bigquery"`, 1), `"mc_managed"`, `"aws_secrets_manager"`, 1) + `}`,
	"GET /api/v2/connections/cn-3":                   `{"id":"cn-3","credentials_id":null,` + connectionOut + `}`,
	"PATCH /api/v2/connections/cn-1":                 `{"id":"cn-1","credentials_id":"cr-1",` + strings.Replace(connectionOut, `"name":"n"`, `"name":"z"`, 1) + `}`,
	"GET /api/v2/credentials/snowflake/cr-1":         `{"id":"cr-1","connection_type":"snowflake","storage_type":"mc_managed","created_time":"2026-09-23T00:00:00Z","account":"a","user":"u","warehouse":"w"}`,
	"PATCH /api/v2/credentials/snowflake/cr-1":       `{"id":"cr-1","connection_type":"snowflake","storage_type":"mc_managed","created_time":"2026-09-23T00:00:00Z","account":"a","user":"u","warehouse":"w"}`,
	"GET /api/v2/credentials/self-hosted/aws/cr-2":   `{"id":"cr-2","connection_type":"bigquery","storage_type":"aws_secrets_manager","created_time":"2026-09-23T00:00:00Z","bq_project_id":"p","databricks_warehouse_id":null,"aws_secret":"s","aws_region":null,"assumable_role":null,"external_id":null}`,
	"PATCH /api/v2/credentials/self-hosted/aws/cr-2": `{"id":"cr-2","connection_type":"bigquery","storage_type":"aws_secrets_manager","created_time":"2026-09-23T00:00:00Z","bq_project_id":"p","databricks_warehouse_id":null,"aws_secret":"s","aws_region":null,"assumable_role":null,"external_id":null}`,
}

func newFakeConnectionsAPI(t *testing.T, fail map[string]int) (*fakeConnectionsAPI, *httptest.Server) {
	t.Helper()
	f := &fakeConnectionsAPI{t: t, fail: fail, bodies: map[string]map[string]any{}, queries: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeConnectionsAPI) serve(w http.ResponseWriter, r *http.Request) {
	call := r.Method + " " + r.URL.Path
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.queries[call] = r.URL.RawQuery
	if len(raw) > 0 {
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.bodies[call] = body
	}
	f.mu.Unlock()
	if status, ok := f.fail[call]; ok {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"code":"refused","detail":"The API refused it.","request_id":"req-1","status":` + strconv.Itoa(status) + `}`))
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/validate") {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/api/v2/validations/run-1")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(validationRunJSON("running", "pending", "null", "[]")))
		return
	}
	if call == "GET /api/v2/validations/run-1" {
		passed, errs := "true", "[]"
		if f.refuse {
			passed, errs = "false", `[{"friendly_message":"The key was rejected.","resolution":"Check the user's public key."}]`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validationRunJSON("completed", "completed", passed, errs)))
		return
	}
	if call == "GET /api/v2/connections" {
		items := ""
		if f.occupied {
			items = `{"id":"cn-9","credentials_id":null,` + connectionOut + `}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[` + items + `],"next_cursor":null,"has_more":false,"count":null}`))
		return
	}
	if call == "GET /api/v2/warehouses/wh-9" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"wh-9","name":"w","type":"snowflake","deployment_id":"d9","created_time":"2026-09-23T00:00:00Z"}`))
		return
	}
	if body, ok := readBodies[call]; ok {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
		return
	}
	body, ok := createdBodies[call]
	if !ok {
		f.t.Errorf("unexpected request %s", call)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(body))
}

func validationRunJSON(runStatus, status, passed, errs string) string {
	return `{"id":"run-1","status":"` + runStatus + `","revision":1,"target_type":"credentials","target_id":null,` +
		`"validations_passed":0,"validations_total":1,"started_at":"2026-09-23T00:00:00Z","finished_at":null,` +
		`"expires_at":"2026-09-23T02:00:00Z","validations":[{"name":"connect","description":"Connect to the warehouse",` +
		`"status":"` + status + `","is_prerequisite":true,"passed":` + passed + `,"errors":` + errs + `,"warnings":[],` +
		`"additional_data":null,"truncated":false}]}`
}

// fastPolling shrinks the validation polling budget for the duration of the test.
func fastPolling(t *testing.T) {
	prev := []time.Duration{validationPollInterval, validationFrameRate}
	validationPollInterval, validationFrameRate = 5*time.Millisecond, time.Millisecond
	t.Cleanup(func() { validationPollInterval, validationFrameRate = prev[0], prev[1] })
}

func addArgs(srv *httptest.Server, t *testing.T, args ...string) []string {
	return append([]string{
		"connections", "add",
		"--endpoint", srv.URL, "--api-id", "i", "--api-token", "s", "--config-dir", t.TempDir(),
		"--output", "json",
	}, args...)
}

func snowflakeArgs(srv *httptest.Server, t *testing.T, extra ...string) []string {
	args := addArgs(srv, t)
	args = append(args[:2], append([]string{"snowflake"}, args[2:]...)...)
	return append(args, append([]string{"--name", "n", "--account", "a", "--user", "u", "--private-key", "k"}, extra...)...)
}

// createArgs is snowflakeArgs with validation skipped, for the tests about creating.
func createArgs(srv *httptest.Server, t *testing.T, extra ...string) []string {
	return snowflakeArgs(srv, t, append([]string{"--skip-validations"}, extra...)...)
}

func assertCalls(t *testing.T, f *fakeConnectionsAPI, want ...string) {
	t.Helper()
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n  %s\nwant:\n  %s", strings.Join(f.calls, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestConnectionsAddCreatesTheWarehouseCredentialsAndConnectionInTurn(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	stdout, _, err := executeStreams(t, createArgs(srv, t, "--deployment-id", "d", "--job-types", "metadata")...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "POST /api/v2/warehouses", "POST /api/v2/credentials/snowflake", "POST /api/v2/connections")
	if got := f.bodies["POST /api/v2/warehouses"]; got["connection_type"] != "snowflake" || got["name"] != "n" || got["deployment_id"] != "d" || got["type"] != nil {
		t.Errorf("warehouse body = %v", got)
	}
	if got := f.bodies["POST /api/v2/credentials/snowflake"]; got["account"] != "a" || got["private_key"] != "k" {
		t.Errorf("credentials body = %v", got)
	}
	got := f.bodies["POST /api/v2/connections"]
	if got["warehouse_id"] != "wh-1" || got["credentials_id"] != "cr-1" || got["name"] != "n" {
		t.Errorf("connection body = %v", got)
	}
	if jobs, _ := got["job_types"].([]any); len(jobs) != 1 || jobs[0] != "metadata" {
		t.Errorf("job_types = %v", got["job_types"])
	}
	if !strings.Contains(stdout, `"id": "cn-1"`) {
		t.Errorf("stdout = %s", stdout)
	}
}

func TestConnectionsAddUndoesWhatItCreatedWhenAStepFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(*httptest.Server, *testing.T) []string
		fail map[string]int
		want []string
		says []string
	}{
		{
			name: "native credentials refused",
			args: func(srv *httptest.Server, t *testing.T) []string {
				return createArgs(srv, t, "--deployment-id", "d")
			},
			fail: map[string]int{"POST /api/v2/credentials/snowflake": http.StatusInternalServerError},
			want: []string{"POST /api/v2/warehouses", "POST /api/v2/credentials/snowflake", "DELETE /api/v2/warehouses/wh-1"},
			says: []string{"The API refused it.", "may have been stored; check montecarlo credentials list", "deleted the warehouse wh-1"},
		},
		{
			name: "native connection refused",
			args: func(srv *httptest.Server, t *testing.T) []string {
				return createArgs(srv, t, "--deployment-id", "d")
			},
			fail: map[string]int{"POST /api/v2/connections": http.StatusUnprocessableEntity},
			want: []string{
				"POST /api/v2/warehouses", "POST /api/v2/credentials/snowflake", "POST /api/v2/connections",
				"DELETE /api/v2/credentials/snowflake/cr-1", "DELETE /api/v2/warehouses/wh-1",
			},
			says: []string{"deleted the credentials cr-1", "deleted the warehouse wh-1"},
		},
		{
			name: "self-hosted connection refused",
			args: func(srv *httptest.Server, t *testing.T) []string {
				return addArgs(srv, t, "bigquery", "--name", "n", "--deployment-id", "d", "--self-hosted-aws-secret", "s", "--bq-project-id", "p", "--skip-validations")
			},
			fail: map[string]int{"POST /api/v2/connections": http.StatusUnprocessableEntity},
			want: []string{
				"POST /api/v2/warehouses", "POST /api/v2/credentials/self-hosted/aws", "POST /api/v2/connections",
				"DELETE /api/v2/credentials/self-hosted/aws/cr-2", "DELETE /api/v2/warehouses/wh-1",
			},
			says: []string{"deleted the credentials cr-2", "deleted the warehouse wh-1"},
		},
		{
			name: "a given warehouse is never deleted",
			args: func(srv *httptest.Server, t *testing.T) []string {
				return createArgs(srv, t, "--warehouse-id", "wh-9")
			},
			fail: map[string]int{"POST /api/v2/connections": http.StatusUnprocessableEntity},
			want: []string{"POST /api/v2/credentials/snowflake", "POST /api/v2/connections", "DELETE /api/v2/credentials/snowflake/cr-1"},
			says: []string{"deleted the credentials cr-1"},
		},
		{
			name: "a cleanup delete refused",
			args: func(srv *httptest.Server, t *testing.T) []string {
				return createArgs(srv, t, "--deployment-id", "d")
			},
			fail: map[string]int{"POST /api/v2/connections": http.StatusUnprocessableEntity, "DELETE /api/v2/warehouses/wh-1": http.StatusConflict},
			want: []string{
				"POST /api/v2/warehouses", "POST /api/v2/credentials/snowflake", "POST /api/v2/connections",
				"DELETE /api/v2/credentials/snowflake/cr-1", "DELETE /api/v2/warehouses/wh-1",
			},
			says: []string{"could not delete the warehouse wh-1", "delete it with: montecarlo warehouses delete wh-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeConnectionsAPI(t, tc.fail)
			stdout, _, err := executeStreams(t, tc.args(srv, t)...)
			if err == nil {
				t.Fatal("the command succeeded")
			}
			assertCalls(t, f, tc.want...)
			for _, s := range tc.says {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("err lacks %q:\n%v", s, err)
				}
			}
			if stdout != "" {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}
}

func TestConnectionsAddPointsAtWarehouseIdWhenTheWarehouseNameIsTaken(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, map[string]int{"POST /api/v2/warehouses": http.StatusConflict})
	_, _, err := executeStreams(t, createArgs(srv, t, "--deployment-id", "d")...)
	assertCalls(t, f, "POST /api/v2/warehouses")
	if err == nil || !strings.Contains(err.Error(), "montecarlo warehouses list") || !strings.Contains(err.Error(), "--warehouse-id") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionsAddRefusesBadFlagsBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(*httptest.Server, *testing.T) []string
		says string
	}{
		{"no credentials", func(srv *httptest.Server, t *testing.T) []string {
			return addArgs(srv, t, "snowflake", "--name", "n", "--deployment-id", "d")
		}, "pass the flags of one of: snowflake, self-hosted-aws"},
		{"two credentials", func(srv *httptest.Server, t *testing.T) []string {
			return snowflakeArgs(srv, t, "--deployment-id", "d", "--self-hosted-aws-secret", "s")
		}, "belong to different alternatives"},
		{"a shared flag with native credentials", func(srv *httptest.Server, t *testing.T) []string {
			return snowflakeArgs(srv, t, "--deployment-id", "d", "--bq-project-id", "p")
		}, "snowflake credentials take no --bq-project-id"},
		{"both warehouse flags", func(srv *httptest.Server, t *testing.T) []string {
			return snowflakeArgs(srv, t, "--deployment-id", "d", "--warehouse-id", "w")
		}, "pass --deployment-id to create a warehouse"},
		{"neither warehouse flag", func(srv *httptest.Server, t *testing.T) []string {
			return snowflakeArgs(srv, t)
		}, "pass --deployment-id to create a warehouse"},
		{"a group missing a required flag", func(srv *httptest.Server, t *testing.T) []string {
			return addArgs(srv, t, "snowflake", "--name", "n", "--deployment-id", "d", "--account", "a", "--private-key", "k")
		}, "--user is required"},
		{"both validation flags", func(srv *httptest.Server, t *testing.T) []string {
			return snowflakeArgs(srv, t, "--deployment-id", "d", "--validate-only", "--skip-validations")
		}, "pass --validate-only or --skip-validations, not both"},
		{"no name when creating", func(srv *httptest.Server, t *testing.T) []string {
			return addArgs(srv, t, "snowflake", "--deployment-id", "d", "--account", "a", "--user", "u", "--private-key", "k")
		}, "--name is required"},
		{"native flags on the parent", func(srv *httptest.Server, t *testing.T) []string {
			return addArgs(srv, t, "bigquery", "--name", "n", "--deployment-id", "d", "--account", "a")
		}, "unknown flag: --account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeConnectionsAPI(t, nil)
			_, _, err := executeStreams(t, tc.args(srv, t)...)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want it to say %q", err, tc.says)
			}
			assertCalls(t, f)
		})
	}
}

func TestConnectionsAddValidatesThenAsksThenCreates(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	_, stderr, err := executeStreams(t, snowflakeArgs(srv, t, "--deployment-id", "d", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"POST /api/v2/credentials/snowflake/validate", "GET /api/v2/validations/run-1",
		"POST /api/v2/warehouses", "POST /api/v2/credentials/snowflake", "POST /api/v2/connections")
	if got := f.bodies["POST /api/v2/credentials/snowflake/validate"]; got["deployment_id"] != "d" || got["account"] != "a" || got["private_key"] != "k" {
		t.Errorf("validate body = %v", got)
	}
	if !strings.Contains(stderr, "  ✓ Connect to the warehouse") || !strings.Contains(stderr, "1 of 1 validations passed (run run-1).") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestConnectionsAddCreatesNothingWhenAValidationFails(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	f.refuse = true
	stdout, stderr, err := executeStreams(t, snowflakeArgs(srv, t, "--deployment-id", "d", "--yes")...)
	assertCalls(t, f, "POST /api/v2/credentials/snowflake/validate", "GET /api/v2/validations/run-1")
	if err == nil || !strings.Contains(err.Error(), "nothing was created") || !strings.Contains(err.Error(), "--skip-validations") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(stderr, "The key was rejected.") || stdout != "" {
		t.Errorf("stdout %q, stderr %s", stdout, stderr)
	}
}

func TestConnectionsAddValidateOnlyCreatesNothingAndNeedsNoName(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	_, stderr, err := executeStreams(t, addArgs(srv, t, "snowflake", "--deployment-id", "d", "--account", "a", "--user", "u", "--private-key", "k", "--validate-only")...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "POST /api/v2/credentials/snowflake/validate", "GET /api/v2/validations/run-1")
	if !strings.Contains(stderr, "Nothing was created: --validate-only.") {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestConnectionsAddValidatesFromTheDeploymentOfAGivenWarehouse(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	if _, _, err := executeStreams(t, snowflakeArgs(srv, t, "--warehouse-id", "wh-9", "--yes")...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"GET /api/v2/warehouses/wh-9", "POST /api/v2/credentials/snowflake/validate", "GET /api/v2/validations/run-1",
		"POST /api/v2/credentials/snowflake", "POST /api/v2/connections")
	if got := f.bodies["POST /api/v2/credentials/snowflake/validate"]["deployment_id"]; got != "d9" {
		t.Errorf("deployment_id = %v", got)
	}
}

func TestConnectionsAddValidatesSelfHostedCredentialsWithTheConnectionType(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	args := addArgs(srv, t, "bigquery", "--deployment-id", "d", "--self-hosted-aws-secret", "s", "--bq-project-id", "p", "--validate-only")
	if _, _, err := executeStreams(t, args...); err != nil {
		t.Fatal(err)
	}
	got := f.bodies["POST /api/v2/credentials/self-hosted/aws/validate"]
	if got["connection_type"] != "bigquery" || got["aws_secret"] != "s" || got["bq_project_id"] != "p" || got["deployment_id"] != "d" {
		t.Errorf("validate body = %v", got)
	}
}

func TestConnectionsAddAsksBeforeCreatingAndNeedsYesWithoutATerminal(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	_, _, err := executeStreams(t, snowflakeArgs(srv, t, "--deployment-id", "d")...)
	if err == nil || !strings.Contains(err.Error(), `Create snowflake connection "n": pass --yes`) {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "POST /api/v2/credentials/snowflake/validate", "GET /api/v2/validations/run-1")
}

func updateArgs(srv *httptest.Server, t *testing.T, args ...string) []string {
	return append([]string{
		"connections", "update",
		"--endpoint", srv.URL, "--api-id", "i", "--api-token", "s", "--config-dir", t.TempDir(),
		"--output", "json",
	}, args...)
}

func TestConnectionsUpdateValidatesARotatedKeyWithTheStoredFields(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	if _, _, err := executeStreams(t, updateArgs(srv, t, "snowflake", "cn-1", "--private-key", "k2", "--yes")...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"GET /api/v2/connections/cn-1", "GET /api/v2/credentials/snowflake/cr-1",
		"POST /api/v2/credentials/snowflake/validate", "GET /api/v2/validations/run-1",
		"PATCH /api/v2/credentials/snowflake/cr-1")
	v := f.bodies["POST /api/v2/credentials/snowflake/validate"]
	if v["account"] != "a" || v["user"] != "u" || v["warehouse"] != "w" || v["private_key"] != "k2" || v["deployment_id"] != "d" {
		t.Errorf("validate body = %v", v)
	}
	if _, has := v["private_key_passphrase"]; has {
		t.Errorf("a new key without a passphrase validated with one: %v", v)
	}
	if patch := f.bodies["PATCH /api/v2/credentials/snowflake/cr-1"]; len(patch) != 1 || patch["private_key"] != "k2" {
		t.Errorf("patch body = %v", patch)
	}
}

func TestConnectionsUpdateNeedsTheSecretToValidateAChangeWithoutIt(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	_, _, err := executeStreams(t, updateArgs(srv, t, "snowflake", "cn-1", "--user", "u2", "--yes")...)
	if err == nil || !strings.Contains(err.Error(), "needs --private-key") || !strings.Contains(err.Error(), "--skip-validations") {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-1", "GET /api/v2/credentials/snowflake/cr-1")
}

func TestConnectionsUpdateSkipsValidationAndClearsAFieldPassedEmpty(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	if _, _, err := executeStreams(t, updateArgs(srv, t, "snowflake", "cn-1", "--user", "u2", "--warehouse", "", "--skip-validations")...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-1", "PATCH /api/v2/credentials/snowflake/cr-1")
	patch := f.bodies["PATCH /api/v2/credentials/snowflake/cr-1"]
	if w, has := patch["warehouse"]; !has || w != nil || patch["user"] != "u2" || len(patch) != 2 {
		t.Errorf("patch body = %v", patch)
	}
}

func TestConnectionsUpdateRenamesWithoutValidating(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	stdout, _, err := executeStreams(t, updateArgs(srv, t, "cn-1", "--name", "z")...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-1", "PATCH /api/v2/connections/cn-1")
	if f.bodies["PATCH /api/v2/connections/cn-1"]["name"] != "z" || !strings.Contains(stdout, `"name": "z"`) {
		t.Errorf("body %v, stdout %s", f.bodies["PATCH /api/v2/connections/cn-1"], stdout)
	}
}

func TestConnectionsUpdateValidatesASelfHostedChangeFromTheStoredReference(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	if _, _, err := executeStreams(t, updateArgs(srv, t, "cn-2", "--self-hosted-aws-region", "us-east-2", "--validate-only")...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"GET /api/v2/connections/cn-2", "GET /api/v2/credentials/self-hosted/aws/cr-2",
		"POST /api/v2/credentials/self-hosted/aws/validate", "GET /api/v2/validations/run-1")
	v := f.bodies["POST /api/v2/credentials/self-hosted/aws/validate"]
	if v["aws_secret"] != "s" || v["aws_region"] != "us-east-2" || v["connection_type"] != "bigquery" || v["bq_project_id"] != "p" {
		t.Errorf("validate body = %v", v)
	}
}

func TestConnectionsUpdateSaysTheCredentialsChangedWhenTheRenameFails(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, map[string]int{"PATCH /api/v2/connections/cn-1": http.StatusConflict})
	_, _, err := executeStreams(t, updateArgs(srv, t, "snowflake", "cn-1", "--user", "u2", "--name", "z", "--skip-validations")...)
	if err == nil || !strings.Contains(err.Error(), "The credentials were changed; the connection was not renamed.") {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-1", "PATCH /api/v2/credentials/snowflake/cr-1", "PATCH /api/v2/connections/cn-1")
}

func TestConnectionsUpdateRefusesBeforeChangingAnything(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		says string
	}{
		{"nothing to change", []string{"cn-1"}, "nothing to update"},
		{"two credentials groups", []string{"cn-1", "--self-hosted-aws-region", "r", "--self-hosted-gcp-secret", "g"}, "belong to different alternatives"},
		{"validate-only with no credentials change", []string{"cn-1", "--name", "z", "--validate-only"}, "--validate-only validates a credentials change"},
		{"self-hosted flags on a native subcommand", []string{"snowflake", "cn-1", "--self-hosted-aws-region", "r"}, "unknown flag: --self-hosted-aws-region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeConnectionsAPI(t, nil)
			_, _, err := executeStreams(t, updateArgs(srv, t, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("err = %v, want %q", err, tc.says)
			}
			assertCalls(t, f)
		})
	}
	f, srv := newFakeConnectionsAPI(t, nil)
	_, _, err := executeStreams(t, updateArgs(srv, t, "snowflake", "cn-3", "--user", "u2", "--skip-validations")...)
	if err == nil || !strings.Contains(err.Error(), "has no credentials this command can change") {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-3")
}

func validateArgs(srv *httptest.Server, t *testing.T, args ...string) []string {
	return append([]string{
		"--endpoint", srv.URL, "--api-id", "i", "--api-token", "s", "--config-dir", t.TempDir(), "--output", "json",
	}, args...)
}

func TestConnectionsValidateFollowsTheRunAndPrintsWhereItEnded(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	stdout, stderr, err := executeStreams(t, append([]string{"connections", "validate", "cn-1"}, validateArgs(srv, t)...)...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "POST /api/v2/connections/cn-1/validate", "GET /api/v2/validations/run-1")
	if !strings.Contains(stderr, "1 of 1 validations passed (run run-1).") || !strings.Contains(stdout, `"status": "completed"`) {
		t.Errorf("stdout %s\nstderr %s", stdout, stderr)
	}
}

func TestConnectionsValidateFailsWhenAValidationDidNotPass(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	f.refuse = true
	stdout, _, err := executeStreams(t, append([]string{"connections", "validate", "cn-1"}, validateArgs(srv, t)...)...)
	if err == nil || err.Error() != "not every validation passed" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(stdout, `"passed": false`) {
		t.Errorf("stdout = %s", stdout)
	}
}

func TestConnectionsValidateNoWaitPrintsTheRunAsItStarts(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	stdout, _, err := executeStreams(t, append([]string{"connections", "validate", "cn-1", "--no-wait"}, validateArgs(srv, t)...)...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "POST /api/v2/connections/cn-1/validate")
	if !strings.Contains(stdout, `"status": "running"`) {
		t.Errorf("stdout = %s", stdout)
	}
}

func TestCredentialsValidateFollowsTheRunToo(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	args := append([]string{"credentials", "validate", "snowflake", "--deployment-id", "d", "--account", "a", "--user", "u", "--private-key", "k"}, validateArgs(srv, t)...)
	if _, _, err := executeStreams(t, args...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "POST /api/v2/credentials/snowflake/validate", "GET /api/v2/validations/run-1")
}

func deleteArgs(srv *httptest.Server, t *testing.T, args ...string) []string {
	return append([]string{
		"connections", "delete",
		"--endpoint", srv.URL, "--api-id", "i", "--api-token", "s", "--config-dir", t.TempDir(),
	}, args...)
}

func TestConnectionsDeleteDeletesTheCredentialsAndNotesAnEmptyWarehouse(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	stdout, stderr, err := executeStreams(t, deleteArgs(srv, t, "cn-1", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"GET /api/v2/connections/cn-1", "DELETE /api/v2/connections/cn-1",
		"DELETE /api/v2/credentials/cr-1", "GET /api/v2/connections")
	if q := f.queries["GET /api/v2/connections"]; !strings.Contains(q, "warehouse_id=wh-1") || !strings.Contains(q, "limit=1") {
		t.Errorf("list query = %q", q)
	}
	if !strings.Contains(stderr, "The warehouse wh-1 has no connection left") || !strings.Contains(stderr, "warehouses delete wh-1") || !strings.Contains(stderr, "--with-warehouse") {
		t.Errorf("stderr = %q", stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestConnectionsDeleteSaysNothingOfAWarehouseWithConnectionsLeft(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	f.occupied = true
	_, stderr, err := executeStreams(t, deleteArgs(srv, t, "cn-1", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestConnectionsDeleteKeepsTheCredentialsAndDeletesTheWarehouseWhenTold(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	_, stderr, err := executeStreams(t, deleteArgs(srv, t, "cn-1", "--keep-credentials", "--with-warehouse", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-1", "GET /api/v2/connections", "DELETE /api/v2/connections/cn-1", "DELETE /api/v2/warehouses/wh-1")
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestConnectionsDeleteKeepsCredentialsAnotherConnectionUses(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, map[string]int{"DELETE /api/v2/credentials/cr-1": http.StatusConflict})
	f.occupied = true
	_, stderr, err := executeStreams(t, deleteArgs(srv, t, "cn-1", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Kept the credentials cr-1:") || !strings.Contains(stderr, "The API refused it.") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestConnectionsDeleteNamesWhatIsLeftWhenALaterDeleteFails(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, map[string]int{
		"DELETE /api/v2/credentials/cr-1": http.StatusInternalServerError,
		"DELETE /api/v2/warehouses/wh-1":  http.StatusConflict,
	})
	_, _, err := executeStreams(t, deleteArgs(srv, t, "cn-1", "--with-warehouse", "--yes")...)
	if err == nil {
		t.Fatal("the command succeeded")
	}
	for _, want := range []string{
		"the connection cn-1 was deleted, but:",
		"could not delete the credentials cr-1: ",
		"delete them with: " + binaryName + " credentials delete cr-1",
		"could not delete the warehouse wh-1: ",
		"delete it with: " + binaryName + " warehouses delete wh-1",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err lacks %q:\n%v", want, err)
		}
	}
	assertCalls(t, f,
		"GET /api/v2/connections/cn-1", "GET /api/v2/connections", "DELETE /api/v2/connections/cn-1",
		"DELETE /api/v2/credentials/cr-1", "DELETE /api/v2/warehouses/wh-1")
}

func TestConnectionsDeleteLeavesTheRestWhenTheConnectionDeleteFails(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, map[string]int{"DELETE /api/v2/connections/cn-1": http.StatusConflict})
	_, _, err := executeStreams(t, deleteArgs(srv, t, "cn-1", "--with-warehouse", "--yes")...)
	if err == nil || strings.Contains(err.Error(), "was deleted") {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-1", "GET /api/v2/connections", "DELETE /api/v2/connections/cn-1")
}

func TestConnectionsDeleteHasNoCredentialsToDeleteForAConnectionWithout(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	f.occupied = true
	if _, _, err := executeStreams(t, deleteArgs(srv, t, "cn-3", "--yes")...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-3", "DELETE /api/v2/connections/cn-3", "GET /api/v2/connections")
}

func TestConnectionsDeleteAsksNamingWhatGoesWithTheConnection(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{nil, "Delete connection cn-1 and its credentials: pass --yes"},
		{[]string{"--keep-credentials"}, "Delete connection cn-1: pass --yes"},
		{[]string{"--keep-credentials", "--with-warehouse"}, "Delete connection cn-1 and its warehouse: pass --yes"},
		{[]string{"--with-warehouse"}, "Delete connection cn-1, its credentials and its warehouse: pass --yes"},
	} {
		f, srv := newFakeConnectionsAPI(t, nil)
		_, _, err := executeStreams(t, deleteArgs(srv, t, append([]string{"cn-1"}, tc.flags...)...)...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v", tc.flags, err)
		}
		assertCalls(t, f)
	}
}

func TestConnectionsDeleteWithWarehouseRefusesBeforeDeletingWhileAnotherConnectionUsesIt(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	f.occupied = true
	_, _, err := executeStreams(t, deleteArgs(srv, t, "cn-1", "--with-warehouse", "--yes")...)
	if err == nil || !strings.Contains(err.Error(), "cn-9") {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-1", "GET /api/v2/connections")
}

func TestConnectionsUpdateChangesASharedSelfHostedFieldAlone(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	if _, _, err := executeStreams(t, updateArgs(srv, t, "cn-2", "--bq-project-id", "p2", "--skip-validations")...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-2", "PATCH /api/v2/credentials/self-hosted/aws/cr-2")
	if patch := f.bodies["PATCH /api/v2/credentials/self-hosted/aws/cr-2"]; len(patch) != 1 || patch["bq_project_id"] != "p2" {
		t.Errorf("patch body = %v", patch)
	}
}

func TestConnectionsUpdateRefusesCredentialsOfAnotherStorageOrType(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"self-hosted flags on a managed connection", []string{"cn-1", "--self-hosted-aws-secret", "s2", "--skip-validations"}, "mc_managed"},
		{"another storage's flags", []string{"cn-2", "--self-hosted-env-var-name", "V", "--skip-validations"}, "aws_secrets_manager"},
		{"a native type on another type's connection", []string{"snowflake", "cn-2", "--user", "u2", "--skip-validations"}, "bigquery"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeConnectionsAPI(t, nil)
			_, _, err := executeStreams(t, updateArgs(srv, t, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			for _, c := range f.calls {
				if strings.HasPrefix(c, "PATCH") {
					t.Fatalf("changed something: %v", f.calls)
				}
			}
		})
	}
}
