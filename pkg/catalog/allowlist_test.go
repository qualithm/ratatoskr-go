package catalog_test

import (
	"strings"
	"testing"

	"github.com/qualithm/ratatoskr-go/pkg/catalog"
)

func TestLoadAllowlistEmpty(t *testing.T) {
	t.Parallel()
	a, err := catalog.LoadAllowlist(nil)
	if err != nil {
		t.Fatalf("nil reader: %v", err)
	}
	if ok, _ := a.AllowsMetric("anything"); ok {
		t.Fatal("empty allowlist must not allow anything")
	}

	a2, err := catalog.LoadAllowlist(strings.NewReader(""))
	if err != nil {
		t.Fatalf("empty body: %v", err)
	}
	if ok, _ := a2.AllowsMetric("anything"); ok {
		t.Fatal("empty body must not allow anything")
	}
}

func TestLoadAllowlistMetricsAndLabels(t *testing.T) {
	t.Parallel()
	const body = `
metrics:
  - pattern: cortex_*
    reason: "Cortex internals intentionally not in catalog"
  - pattern: legacy_metric
labels:
  - metric: cortex_*
    patterns: [tenant, user]
    reason: "tenant labels stripped"
label_values:
  - metric: up
    label: job
    patterns: [synthetic-*]
`
	a, err := catalog.LoadAllowlist(strings.NewReader(body))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if ok, reason := a.AllowsMetric("cortex_request_duration_seconds_count"); !ok {
		t.Fatal("cortex_* prefix should match")
	} else if reason == "" {
		t.Fatal("expected reason for cortex_* match")
	}
	if ok, _ := a.AllowsMetric("legacy_metric"); !ok {
		t.Fatal("exact metric match failed")
	}
	if ok, _ := a.AllowsMetric("legacy_metric_v2"); ok {
		t.Fatal("exact metric must not match prefix")
	}

	if ok, _ := a.AllowsLabel("cortex_query_seconds_count", "tenant"); !ok {
		t.Fatal("label-on-metric-prefix should match")
	}
	if ok, _ := a.AllowsLabel("loki_panic_total", "tenant"); ok {
		t.Fatal("label allow leaked across metrics")
	}

	if ok, _ := a.AllowsLabelValue("up", "job", "synthetic-canary"); !ok {
		t.Fatal("label-value prefix should match")
	}
	if ok, _ := a.AllowsLabelValue("up", "job", "real"); ok {
		t.Fatal("label-value allow leaked")
	}
}

func TestLoadAllowlistBadYAML(t *testing.T) {
	t.Parallel()
	_, err := catalog.LoadAllowlist(strings.NewReader("{not valid"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestAllowlistNilSafe(t *testing.T) {
	t.Parallel()
	var a *catalog.Allowlist
	if ok, _ := a.AllowsMetric("x"); ok {
		t.Fatal("nil allowlist must not allow")
	}
	if ok, _ := a.AllowsLabel("x", "y"); ok {
		t.Fatal("nil allowlist must not allow")
	}
	if ok, _ := a.AllowsLabelValue("x", "y", "z"); ok {
		t.Fatal("nil allowlist must not allow")
	}
}
