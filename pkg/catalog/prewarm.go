package catalog

import (
	"context"
	"errors"
	"sort"
	"sync"

	ratatoskr "github.com/qualithm/ratatoskr-go"
)

// PrewarmInputs lists the extractions a [Checker] will run against. The
// prewarm walks them once to collect every unique catalog query the
// checker would otherwise issue lazily, and fetches them in parallel.
//
// Either slice may be empty. Nil values are skipped.
type PrewarmInputs struct {
	PromQL []ratatoskr.Result
	LogQL  []ratatoskr.LogQLResult
}

// Prewarm primes the [Checker.Store] with every catalog response the
// inputs would later require, up to parallelism concurrent fetches.
//
// A parallelism of zero or negative defaults to four. Errors from
// individual fetches are collected and joined via [errors.Join]; partial
// results remain in the store so a later checker run benefits from what
// did succeed.
func Prewarm(ctx context.Context, c *Checker, in PrewarmInputs, parallelism int) error {
	if c == nil {
		return errors.New("catalog: nil checker")
	}
	if parallelism <= 0 {
		parallelism = 4
	}

	tasks := plan(c, in)
	if len(tasks) == 0 {
		return nil
	}

	sem := make(chan struct{}, parallelism)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		errs   []error
		cancel context.CancelFunc
	)
	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	for _, t := range tasks {
		t := t
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if err := t(ctx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

type prewarmTask func(ctx context.Context) error

// plan returns the deduplicated set of fetch tasks needed to populate the
// cache for the given inputs. Ordering is deterministic for stable test
// output: PromQL global metric names first, then per-metric label sets,
// then per-(metric,label) value sets, then LogQL.
func plan(c *Checker, in PrewarmInputs) []prewarmTask {
	tasks := []prewarmTask{}

	// PromQL.
	if c.Prom != nil {
		metrics := map[string]struct{}{}
		labelsByMetric := map[string]map[string]struct{}{}
		valuesByMetricLabel := map[string]map[string]map[string]struct{}{}

		for _, r := range in.PromQL {
			for _, m := range r.MetricRefs {
				metrics[m] = struct{}{}
			}
			for _, s := range r.Selectors {
				if s.Metric == "" || s.Label == "" {
					continue
				}
				metrics[s.Metric] = struct{}{}
				if labelsByMetric[s.Metric] == nil {
					labelsByMetric[s.Metric] = map[string]struct{}{}
				}
				labelsByMetric[s.Metric][s.Label] = struct{}{}
				if s.Op == "=" && s.Value != "" {
					if valuesByMetricLabel[s.Metric] == nil {
						valuesByMetricLabel[s.Metric] = map[string]map[string]struct{}{}
					}
					if valuesByMetricLabel[s.Metric][s.Label] == nil {
						valuesByMetricLabel[s.Metric][s.Label] = map[string]struct{}{}
					}
					valuesByMetricLabel[s.Metric][s.Label][s.Value] = struct{}{}
				}
			}
		}

		if len(metrics) > 0 || len(labelsByMetric) > 0 {
			tasks = append(tasks, func(ctx context.Context) error {
				_, err := c.promMetricNames(ctx)
				return err
			})
		}
		for _, metric := range sortedKeys(labelsByMetric) {
			metric := metric
			tasks = append(tasks, func(ctx context.Context) error {
				_, err := c.promMetricLabels(ctx, metric)
				return err
			})
		}
		for _, metric := range sortedKeys(valuesByMetricLabel) {
			for _, label := range sortedKeys(valuesByMetricLabel[metric]) {
				metric, label := metric, label
				tasks = append(tasks, func(ctx context.Context) error {
					_, err := c.promMetricLabelValues(ctx, metric, label)
					return err
				})
			}
		}
	}

	// LogQL.
	if c.Loki != nil {
		labels := map[string]struct{}{}
		valuesByLabel := map[string]map[string]struct{}{}
		for _, r := range in.LogQL {
			for _, s := range r.StreamSelectors {
				if s.Label == "" {
					continue
				}
				labels[s.Label] = struct{}{}
				if s.Op == "=" && s.Value != "" {
					if valuesByLabel[s.Label] == nil {
						valuesByLabel[s.Label] = map[string]struct{}{}
					}
					valuesByLabel[s.Label][s.Value] = struct{}{}
				}
			}
		}
		if len(labels) > 0 {
			tasks = append(tasks, func(ctx context.Context) error {
				_, err := c.lokiLabelNames(ctx)
				return err
			})
		}
		for _, label := range sortedKeys(valuesByLabel) {
			label := label
			tasks = append(tasks, func(ctx context.Context) error {
				_, err := c.lokiLabelValues(ctx, label)
				return err
			})
		}
	}

	return tasks
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
