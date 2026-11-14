package applog

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sort"
)

// QueryFile reads a daily JSONL access log and applies the same filters as the
// in-memory ring. Results are returned newest first.
func (a *AccessLog) QueryFile(date string, f Filter) ([]Entry, int64, error) {
	path, err := a.FileForDate(date)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 || f.PageSize > 500 {
		f.PageSize = 50
	}
	// Keep only the newest window needed for this page instead of all matches.
	// Files are append-only in time order; sort just the window to tolerate a
	// slightly out-of-order writer flush.
	need := (f.Page-1)*f.PageSize + f.PageSize
	const maxWindow = 10000
	if need > maxWindow {
		return nil, 0, errors.New("log page window is too large")
	}
	window := make([]Entry, need)
	var total int64
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		e.Path = RedactRequestURI(e.Path)
		if matchesFilter(e, f) {
			window[total%int64(need)] = e
			total++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	n := int(total)
	startOffset := 0
	if n > need {
		n = need
		startOffset = int(total % int64(need))
	}
	matches := make([]Entry, 0, n)
	for i := 0; i < n; i++ {
		matches = append(matches, window[(startOffset+i)%need])
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Time.After(matches[j].Time) })
	start := (f.Page - 1) * f.PageSize
	if start >= len(matches) {
		return []Entry{}, total, nil
	}
	end := start + f.PageSize
	if end > len(matches) {
		end = len(matches)
	}
	return matches[start:end], total, nil
}
