package catalog

import "sort"

// Suggest returns up to maxResults candidates from corpus that are closest
// to query by Levenshtein distance. Candidates more than maxDistance edits
// away are omitted.
//
// When several candidates tie on distance, they are returned in
// lexicographic order so suggestions are deterministic across runs. An
// empty corpus or non-positive maxResults returns nil.
//
// This is the workhorse behind "did you mean `cortex_request_duration_seconds_count`?"
// hints attached to E101/E102/E201 findings.
func Suggest(query string, corpus []string, maxResults, maxDistance int) []string {
	if query == "" || len(corpus) == 0 || maxResults <= 0 {
		return nil
	}
	if maxDistance < 0 {
		maxDistance = 0
	}

	type scored struct {
		name string
		dist int
	}
	scoredAll := make([]scored, 0, len(corpus))
	for _, c := range corpus {
		d := levenshtein(query, c, maxDistance)
		if d < 0 {
			continue
		}
		scoredAll = append(scoredAll, scored{name: c, dist: d})
	}
	sort.Slice(scoredAll, func(i, j int) bool {
		if scoredAll[i].dist != scoredAll[j].dist {
			return scoredAll[i].dist < scoredAll[j].dist
		}
		return scoredAll[i].name < scoredAll[j].name
	})

	n := maxResults
	if len(scoredAll) < n {
		n = len(scoredAll)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = scoredAll[i].name
	}
	return out
}

// levenshtein returns the edit distance between a and b, or -1 if the
// distance exceeds cutoff. The cutoff allows early termination on rows
// whose minimum value already exceeds the threshold, which keeps suggester
// runs cheap against large corpora.
func levenshtein(a, b string, cutoff int) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if abs(la-lb) > cutoff {
		return -1
	}
	if la == 0 {
		if lb <= cutoff {
			return lb
		}
		return -1
	}
	if lb == 0 {
		if la <= cutoff {
			return la
		}
		return -1
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > cutoff {
			return -1
		}
		prev, curr = curr, prev
	}
	d := prev[lb]
	if d > cutoff {
		return -1
	}
	return d
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
