// Example: extract structural references from a PromQL expression.
//
//	go run ./examples/basic_usage
package main

import (
	"encoding/json"
	"fmt"
	"os"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

func main() {
	exprs := []string{
		`http_requests_total{job="api",status=~"5.."}`,
		`sum(rate(node_cpu_seconds_total{mode!="idle"}[5m])) by (instance)`,
		`label_replace(rate(kubelet_volume_stats_used_bytes[5m]), "vol", "$1", "persistentvolumeclaim", "(.+)")`,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	for _, e := range exprs {
		fmt.Printf("--- %s\n", e)
		r, err := ratatoskr.ExtractPromQL(e)
		if err != nil {
			fmt.Fprintln(os.Stderr, "parse:", err)
			continue
		}
		_ = enc.Encode(r)
	}
}
