// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

var errStep = errors.New("the step failed")

// unwindFixture is a command with a live context and captured streams, and an unwind for it.
// The retry budget shrinks for the duration of the test so a retried delete is quick.
func unwindFixture(t *testing.T) (*unwind, *cobra.Command, context.CancelFunc, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	prev := []time.Duration{transientRetryTimeout, transientRetryInterval, retryAfterFloor, undoTimeout}
	transientRetryTimeout, transientRetryInterval, retryAfterFloor = 2*time.Second, 10*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() {
		transientRetryTimeout, transientRetryInterval, retryAfterFloor, undoTimeout = prev[0], prev[1], prev[2], prev[3]
	})
	cmd := &cobra.Command{Use: "t"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cmd.SetContext(ctx)
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	return newUnwind(cmd), cmd, cancel, &stdout, &stderr
}

// deletes records the order resources are deleted in, and answers each with a scripted result.
type deletes struct {
	mu    sync.Mutex
	order []string
}

func (d *deletes) answer(id string, results ...func(ctx context.Context) (*http.Response, error)) func(ctx context.Context) (*http.Response, error) {
	n := 0
	return func(ctx context.Context) (*http.Response, error) {
		d.mu.Lock()
		d.order = append(d.order, id)
		d.mu.Unlock()
		if n >= len(results) {
			return &http.Response{StatusCode: http.StatusNoContent}, nil
		}
		n++
		return results[n-1](ctx)
	}
}

func status(code int) func(context.Context) (*http.Response, error) {
	return func(context.Context) (*http.Response, error) {
		return &http.Response{StatusCode: code, Header: http.Header{}}, errors.New(http.StatusText(code))
	}
}

func TestUnwindDeletesWhatWasCreatedNewestFirst(t *testing.T) {
	for _, tc := range []struct {
		name    string
		created []string
		want    []string
	}{
		{"failing at the first step", nil, nil},
		{"failing at the second step", []string{"w1"}, []string{"w1"}},
		{"failing at the third step", []string{"w1", "c1"}, []string{"c1", "w1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, _, _, stdout, _ := unwindFixture(t)
			var d deletes
			for _, id := range tc.created {
				u.record("thing", id, "things delete "+id, d.answer(id))
			}
			err := u.fail(nil, errStep, "")
			if !errors.Is(err, errStep) {
				t.Fatalf("err = %v, want it to wrap the step's error", err)
			}
			if strings.Join(d.order, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("deleted %v, want %v", d.order, tc.want)
			}
			for _, id := range tc.created {
				if !strings.Contains(err.Error(), "deleted the thing "+id) {
					t.Errorf("err does not report %s: %v", id, err)
				}
			}
			if len(tc.created) == 0 && err.Error() != errStep.Error() {
				t.Errorf("nothing created, yet err = %q", err)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}
}

// TestUnwindStillDeletesAfterTheCommandIsCancelled also proves a delete that needs a retry still
// gets one after Ctrl-C: if undo passed the command's own (cancelled) context to retryWithin, the
// retry would see a done context and give up instead of succeeding on the second try.
func TestUnwindStillDeletesAfterTheCommandIsCancelled(t *testing.T) {
	u, _, cancel, _, _ := unwindFixture(t)
	var d deletes
	var sawDone bool
	u.record("thing", "w1", "things delete w1", d.answer("w1", func(ctx context.Context) (*http.Response, error) {
		sawDone = sawDone || ctx.Err() != nil
		return &http.Response{StatusCode: http.StatusNoContent}, nil
	}))
	u.record("thing", "c1", "things delete c1", d.answer("c1", status(http.StatusTooManyRequests)))
	cancel()
	err := u.fail(nil, context.Canceled, "")
	if strings.Join(d.order, ",") != "c1,c1,w1" || sawDone {
		t.Fatalf("deleted %v, a delete saw a done context: %v", d.order, sawDone)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "deleted the thing c1") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnwindRetriesATransientDeleteAndCountsAMissingResourceAsDeleted(t *testing.T) {
	u, _, _, _, stderr := unwindFixture(t)
	var d deletes
	u.record("warehouse", "w1", "warehouses delete w1", d.answer("w1", status(http.StatusNotFound)))
	u.record("credentials", "c1", "credentials delete c1", d.answer("c1", status(http.StatusTooManyRequests)))
	err := u.fail(nil, errStep, "")
	if strings.Join(d.order, ",") != "c1,c1,w1" {
		t.Fatalf("deleted %v", d.order)
	}
	for _, want := range []string{"deleted the credentials c1", "deleted the warehouse w1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err lacks %q: %v", want, err)
		}
	}
	if !strings.Contains(stderr.String(), "Retrying in") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestUnwindReportsWhatItCouldNotDeleteAndReturnsTheStepError(t *testing.T) {
	u, _, _, _, _ := unwindFixture(t)
	var d deletes
	u.record("warehouse", "w1", "warehouses delete w1", d.answer("w1", status(http.StatusConflict)))
	u.record("credentials", "c1", "credentials delete c1", d.answer("c1"))
	err := u.fail(nil, errStep, "")
	if !errors.Is(err, errStep) {
		t.Fatalf("err = %v", err)
	}
	want := "the step failed\nUndoing what this command created:" +
		"\n  deleted the credentials c1" +
		"\n  could not delete the warehouse w1: Conflict\n    delete it with: " + binaryName + " warehouses delete w1"
	if err.Error() != want {
		t.Fatalf("err =\n%s\nwant\n%s", err, want)
	}
}

func TestUnwindGivesUpOnADeleteThatOutlastsTheCleanupBudget(t *testing.T) {
	u, _, _, _, _ := unwindFixture(t)
	undoTimeout = 50 * time.Millisecond
	u.record("warehouse", "w1", "warehouses delete w1", func(ctx context.Context) (*http.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	start := time.Now()
	err := u.fail(nil, errStep, "")
	if time.Since(start) > time.Second {
		t.Fatalf("cleanup took %s", time.Since(start))
	}
	if !strings.Contains(err.Error(), "could not delete the warehouse w1: context deadline exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnwindSaysAResourceMayExistOnlyWhenTheOutcomeIsUnknown(t *testing.T) {
	const note = "The credentials may have been stored."
	for _, tc := range []struct {
		name string
		resp *http.Response
		want bool
	}{
		{"no response", nil, true},
		{"a server error", &http.Response{StatusCode: http.StatusBadGateway}, true},
		{"unavailable", &http.Response{StatusCode: http.StatusServiceUnavailable}, false},
		{"a refusal", &http.Response{StatusCode: http.StatusUnprocessableEntity}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, _, _, _, _ := unwindFixture(t)
			err := u.fail(tc.resp, errStep, note)
			if got := strings.Contains(err.Error(), note); got != tc.want {
				t.Fatalf("note present = %v, want %v: %v", got, tc.want, err)
			}
			if !errors.Is(err, errStep) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestUnwindNeverReportsTheFailedRequestsBody(t *testing.T) {
	const sentinel = "-----BEGIN PRIVATE KEY-----sentinel"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"validation_failed","detail":"The request is invalid.","request_id":"req-1","status":422,"errors":[{"field":["body","private_key"],"message":"not a key"}]}`))
	}))
	t.Cleanup(srv.Close)
	u, cmd, _, stdout, stderr := unwindFixture(t)
	api, err := sdk.NewClient(context.Background(), sdk.Options{Endpoint: srv.URL, TokenID: "i", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	var d deletes
	u.record("warehouse", "w1", "warehouses delete w1", d.answer("w1"))
	body := sdk.NewSnowflakeCredentialsIn("acct", "user", sentinel)
	_, resp, err := api.CredentialsAPI.CreateSnowflakeCredentials(cmd.Context()).SnowflakeCredentialsIn(*body).Execute()
	if err == nil {
		t.Fatal("the create succeeded")
	}
	failed := u.fail(resp, err, "The credentials may have been stored.")
	for name, text := range map[string]string{"err": failed.Error(), "stdout": stdout.String(), "stderr": stderr.String()} {
		if strings.Contains(text, "sentinel") {
			t.Errorf("%s carries the request body: %q", name, text)
		}
	}
	if !strings.Contains(failed.Error(), "The request is invalid.") || !strings.Contains(failed.Error(), "deleted the warehouse w1") {
		t.Fatalf("err = %v", failed)
	}
}
