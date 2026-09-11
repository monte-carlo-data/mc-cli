package cmd

import (
	"strings"
	"testing"

	sdk "github.com/monte-carlo-data/mc-sdk-go/montecarlo"
	"github.com/spf13/cobra"
)

func TestEnumCompletionListsTheSDKValues(t *testing.T) {
	values, directive := enumCompletion(sdk.AllowedDeploymentTypeEnumValues)(nil, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive %v", directive)
	}
	joined := strings.Join(values, ",")
	if !strings.Contains(joined, "COLLECTION_AGENT") || !strings.Contains(joined, "COLLECTION_DATA_STORE") {
		t.Fatalf("values %v", values)
	}
}

func TestProfileCompletionListsTheSections(t *testing.T) {
	dir := writeProfiles(t, legacyProfiles)
	resetFlags(rootCmd)
	if err := rootCmd.ParseFlags([]string{"--config-dir", dir}); err != nil {
		t.Fatal(err)
	}
	values, directive := profileCompletion(rootCmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp || strings.Join(values, ",") != "default,staging" {
		t.Fatalf("values %v, directive %v", values, directive)
	}
}

func TestCompletionCommandOffersEnumValues(t *testing.T) {
	out, err := execute(t, cobra.ShellCompRequestCmd, "deployments", "create", "--runtime-platform", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AWS", "AZURE", "GCP", "GENERIC"} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("missing %s in:\n%s", want, out)
		}
	}
	out, err = execute(t, cobra.ShellCompRequestCmd, "whoami", "--output", "")
	if err != nil || !strings.Contains(out, "wide\n") {
		t.Fatalf("output completion: %v\n%s", err, out)
	}
}
