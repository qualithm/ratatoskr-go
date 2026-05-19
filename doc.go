// Package ratatoskr extracts structured AST information from PromQL
// expressions, alerting/recording rule files, and (in the future) LogQL
// expressions and Grafana dashboards.
//
// The package is named for the Old Norse Ratatoskr, the squirrel that
// runs up and down Yggdrasil carrying messages between the eagle at the
// crown and the serpent at the roots. The library does the same job for
// query syntax trees: walks them and reports what it finds.
package ratatoskr
