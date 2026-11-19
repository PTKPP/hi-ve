package platform_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hi-ve/backend/internal/platform"
)

func TestPilotCapabilitiesMatchPRD(t *testing.T) {
	prd, err := os.ReadFile("../../../PRD.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(prd), "### 07.7 ")
	if !found {
		t.Fatal("missing P0 registry in PRD §07.7")
	}
	table, _, found := strings.Cut(section, "| **TOTAL**")
	if !found {
		t.Fatal("missing end of P0 registry table")
	}

	var want []string
	idPattern := regexp.MustCompile("`([a-z_]+\\.[a-z_]+)`")
	for line := range strings.Lines(table) {
		if strings.HasPrefix(line, "|") {
			for _, match := range idPattern.FindAllStringSubmatch(line, -1) {
				want = append(want, match[1])
			}
		}
	}
	slices.Sort(want)
	if len(want) != 40 || len(slices.Compact(slices.Clone(want))) != 40 {
		t.Fatalf("expected 40 distinct P0 IDs in PRD, parsed %v", want)
	}
	got := platform.PilotCapabilities()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("registry differs from PRD\ngot  %v\nwant %v", got, want)
	}
	for _, id := range want {
		if !platform.IsPilotCapability(id) {
			t.Errorf("lookup missing approved ID %q", id)
		}
	}
}

func TestLookupExcludesCandidatesAndAliases(t *testing.T) {
	for _, id := range []string{
		"sales.cash_sessions", "customers.directory", "partners.suppliers",
		"retail.inventory.stock_count", "locations.access", "Sales.Cashier", "sales.cashier ", "",
	} {
		if platform.IsPilotCapability(id) {
			t.Errorf("unexpected P0 membership for %q", id)
		}
	}
}

func TestCallerCannotChangeRegistry(t *testing.T) {
	ids := platform.PilotCapabilities()
	original := ids[0]
	ids[0] = "example.unknown"
	if !platform.IsPilotCapability(original) || platform.IsPilotCapability(ids[0]) {
		t.Fatal("mutating returned IDs changed the shared registry")
	}
}
