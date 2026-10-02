package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tableauArgs(srv *httptest.Server, t *testing.T, extra ...string) []string {
	args := addArgs(srv, t)
	args = append(args[:2], append([]string{"tableau"}, args[2:]...)...)
	return append(args, append([]string{"--name", "n", "--server-name", "s", "--username", "u", "--password", "p"}, extra...)...)
}

func TestConnectionsAddCreatesTheBIContainerCredentialsAndConnectionInTurn(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        func(*httptest.Server, *testing.T) []string
		credentials string
	}{
		{"native credentials", func(srv *httptest.Server, t *testing.T) []string {
			return tableauArgs(srv, t, "--deployment-id", "d", "--skip-validations")
		}, "POST /api/v2/credentials/tableau"},
		{"self-hosted credentials", func(srv *httptest.Server, t *testing.T) []string {
			return addArgs(srv, t, "tableau", "--name", "n", "--deployment-id", "d", "--self-hosted-aws-secret", "s", "--skip-validations")
		}, "POST /api/v2/credentials/self-hosted/aws"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeConnectionsAPI(t, nil)
			if _, _, err := executeStreams(t, tc.args(srv, t)...); err != nil {
				t.Fatal(err)
			}
			assertCalls(t, f, "POST /api/v2/bi-containers", tc.credentials, "POST /api/v2/connections")
			if got := f.bodies["POST /api/v2/bi-containers"]; got["type"] != "tableau" || got["name"] != "n" || got["deployment_id"] != "d" {
				t.Errorf("BI container body = %v", got)
			}
			got := f.bodies["POST /api/v2/connections"]
			if _, ok := got["warehouse_id"]; ok || got["bi_container_id"] != "bc-1" {
				t.Errorf("connection body = %v", got)
			}
		})
	}
}

func TestConnectionsAddValidatesFromTheDeploymentOfAGivenBIContainer(t *testing.T) {
	fastPolling(t)
	f, srv := newFakeConnectionsAPI(t, nil)
	if _, _, err := executeStreams(t, tableauArgs(srv, t, "--bi-container-id", "bc-9", "--yes")...); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"GET /api/v2/bi-containers/bc-9", "POST /api/v2/credentials/tableau/validate", "GET /api/v2/validations/run-1",
		"POST /api/v2/credentials/tableau", "POST /api/v2/connections")
	if got := f.bodies["POST /api/v2/credentials/tableau/validate"]; got["deployment_id"] != "d9" || got["server_name"] != "s" {
		t.Errorf("validate body = %v", got)
	}
	if got := f.bodies["POST /api/v2/connections"]["bi_container_id"]; got != "bc-9" {
		t.Errorf("bi_container_id = %v", got)
	}
}

func TestConnectionsAddRefusesABIContainerWithNoDeploymentToValidateFrom(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	_, _, err := executeStreams(t, tableauArgs(srv, t, "--bi-container-id", "bc-0", "--yes")...)
	if err == nil || !strings.Contains(err.Error(), "BI container bc-0 has no deployment") {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "GET /api/v2/bi-containers/bc-0")
}

func TestConnectionsAddUndoesTheBIContainerItCreatedOnly(t *testing.T) {
	refused := map[string]int{"POST /api/v2/connections": http.StatusUnprocessableEntity}
	for _, tc := range []struct {
		name  string
		extra []string
		want  []string
	}{
		{"created here", []string{"--deployment-id", "d"}, []string{
			"POST /api/v2/bi-containers", "POST /api/v2/credentials/tableau", "POST /api/v2/connections",
			"DELETE /api/v2/credentials/tableau/cr-3", "DELETE /api/v2/bi-containers/bc-1",
		}},
		{"given by id", []string{"--bi-container-id", "bc-9"}, []string{
			"POST /api/v2/credentials/tableau", "POST /api/v2/connections", "DELETE /api/v2/credentials/tableau/cr-3",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeConnectionsAPI(t, refused)
			_, _, err := executeStreams(t, tableauArgs(srv, t, append(tc.extra, "--skip-validations")...)...)
			if err == nil {
				t.Fatal("the command succeeded")
			}
			assertCalls(t, f, tc.want...)
		})
	}
}

func TestConnectionsAddPointsAtBIContainerIdWhenTheContainerNameIsTaken(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, map[string]int{"POST /api/v2/bi-containers": http.StatusConflict})
	_, _, err := executeStreams(t, tableauArgs(srv, t, "--deployment-id", "d", "--skip-validations")...)
	assertCalls(t, f, "POST /api/v2/bi-containers")
	if err == nil || !strings.Contains(err.Error(), "montecarlo bi-containers list") || !strings.Contains(err.Error(), "--bi-container-id") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionsAddRefusesTheWrongParentBeforeAnyRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(*httptest.Server, *testing.T) []string
		says string
	}{
		{"a warehouse for a BI type", func(srv *httptest.Server, t *testing.T) []string {
			return tableauArgs(srv, t, "--warehouse-id", "wh-9")
		}, "a tableau connection goes on a BI container, so it takes no --warehouse-id"},
		{"a warehouse for a BI type with self-hosted credentials", func(srv *httptest.Server, t *testing.T) []string {
			return addArgs(srv, t, "tableau", "--name", "n", "--warehouse-id", "wh-9", "--self-hosted-aws-secret", "s")
		}, "a tableau connection goes on a BI container, so it takes no --warehouse-id"},
		{"neither BI container flag", func(srv *httptest.Server, t *testing.T) []string {
			return tableauArgs(srv, t)
		}, "pass --deployment-id to create a BI container"},
		{"both BI container flags", func(srv *httptest.Server, t *testing.T) []string {
			return tableauArgs(srv, t, "--deployment-id", "d", "--bi-container-id", "bc-9")
		}, "pass --deployment-id to create a BI container"},
		{"a BI container for a warehouse type", func(srv *httptest.Server, t *testing.T) []string {
			return snowflakeArgs(srv, t, "--bi-container-id", "bc-9")
		}, "a snowflake connection goes on a warehouse, so it takes no --bi-container-id"},
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

func TestConnectionsDeleteKeepsTheBIContainerAndNotesWhenItIsEmpty(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	_, stderr, err := executeStreams(t, deleteArgs(srv, t, "cn-4", "--yes")...)
	if err != nil {
		t.Fatal(err)
	}
	assertCalls(t, f,
		"GET /api/v2/connections/cn-4", "DELETE /api/v2/connections/cn-4",
		"DELETE /api/v2/credentials/cr-3", "GET /api/v2/connections")
	if q := f.queries["GET /api/v2/connections"]; !strings.Contains(q, "bi_container_id=bc-1") || strings.Contains(q, "warehouse_id") {
		t.Errorf("list query = %q", q)
	}
	if !strings.Contains(stderr, "The BI container bc-1 has no connection left") || !strings.Contains(stderr, "bi-containers delete bc-1") || strings.Contains(stderr, "--with-warehouse") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestConnectionsDeleteRefusesWithWarehouseForABIConnection(t *testing.T) {
	f, srv := newFakeConnectionsAPI(t, nil)
	_, _, err := executeStreams(t, deleteArgs(srv, t, "cn-4", "--with-warehouse", "--yes")...)
	if err == nil || !strings.Contains(err.Error(), "is on BI container bc-1, not on a warehouse, so nothing was deleted") {
		t.Fatalf("err = %v", err)
	}
	assertCalls(t, f, "GET /api/v2/connections/cn-4")
}
