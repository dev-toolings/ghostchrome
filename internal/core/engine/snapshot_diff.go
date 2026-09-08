package engine

import (
	"sort"
	"strconv"
	"strings"
)

// SnapshotDiff reports the changes between two ref maps of a page.
// All fields are optional in JSON output so an unchanged page serialises to
// `{"unchanged":true}`.
type SnapshotDiff struct {
	Unchanged bool                 `json:"unchanged,omitempty"`
	Added     []DiffNode           `json:"added,omitempty"`
	Removed   []string             `json:"removed,omitempty"`
	Changed   map[string]DiffEntry `json:"changed,omitempty"`
	Stats     DiffStats            `json:"stats"`
}

// DiffStats summarises a diff for agent consumption.
type DiffStats struct {
	AddedCount   int `json:"added"`
	RemovedCount int `json:"removed"`
	ChangedCount int `json:"changed"`
	KeptCount    int `json:"kept"`
}

// DiffNode is the minimal payload we return for added nodes.
type DiffNode struct {
	Ref   string `json:"ref"`
	Role  string `json:"role"`
	Name  string `json:"name,omitempty"`
	Href  string `json:"href,omitempty"`
	Value string `json:"value,omitempty"`
}

// DiffEntry captures a single ref that changed between snapshots.
type DiffEntry struct {
	Before DiffNode `json:"before"`
	After  DiffNode `json:"after"`
}

// DiffRefs compares two ref maps (typically the persisted PageSnapshot.Refs).
// Refs are reassigned in document order by the extractor, so a key match
// indicates the same logical node slot. A role or name change on the same key
// counts as "changed"; disappearing or new keys count as removed/added.
func DiffRefs(prev, curr map[string]RefSnapshot) SnapshotDiff {
	diff := SnapshotDiff{Changed: map[string]DiffEntry{}}

	for ref, prevNode := range prev {
		currNode, ok := curr[ref]
		if !ok {
			diff.Removed = append(diff.Removed, ref)
			continue
		}
		if prevNode.Role != currNode.Role || prevNode.Name != currNode.Name {
			diff.Changed[ref] = DiffEntry{
				Before: DiffNode{Ref: ref, Role: prevNode.Role, Name: prevNode.Name},
				After:  DiffNode{Ref: ref, Role: currNode.Role, Name: currNode.Name},
			}
		} else {
			diff.Stats.KeptCount++
		}
	}
	for ref, currNode := range curr {
		if _, existed := prev[ref]; !existed {
			diff.Added = append(diff.Added, DiffNode{Ref: ref, Role: currNode.Role, Name: currNode.Name})
		}
	}

	sort.Strings(diff.Removed)
	sort.Slice(diff.Added, func(i, j int) bool {
		return refLess(diff.Added[i].Ref, diff.Added[j].Ref)
	})

	diff.Stats.AddedCount = len(diff.Added)
	diff.Stats.RemovedCount = len(diff.Removed)
	diff.Stats.ChangedCount = len(diff.Changed)

	if diff.Stats.AddedCount == 0 && diff.Stats.RemovedCount == 0 && diff.Stats.ChangedCount == 0 {
		diff.Unchanged = true
	}
	if len(diff.Changed) == 0 {
		diff.Changed = nil
	}
	return diff
}

// refLess orders "@N" refs numerically (@2 < @10).
func refLess(a, b string) bool {
	ai := refNum(a)
	bi := refNum(b)
	return ai < bi
}

func refNum(ref string) int {
	trimmed := strings.TrimPrefix(ref, "@")
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 1 << 30
	}
	return n
}

// FormatDiff renders a SnapshotDiff as compact text.
func FormatDiff(d SnapshotDiff) string {
	if d.Unchanged {
		return "unchanged"
	}
	var sb strings.Builder
	sb.WriteString("diff ")
	sb.WriteString(strconv.Itoa(d.Stats.AddedCount))
	sb.WriteString("+/")
	sb.WriteString(strconv.Itoa(d.Stats.RemovedCount))
	sb.WriteString("-/")
	sb.WriteString(strconv.Itoa(d.Stats.ChangedCount))
	sb.WriteString("~\n")

	for _, n := range d.Added {
		sb.WriteString("+ ")
		sb.WriteString(n.Ref)
		sb.WriteString(" ")
		sb.WriteString(n.Role)
		if n.Name != "" {
			sb.WriteString(" ")
			sb.WriteString(n.Name)
		}
		sb.WriteString("\n")
	}
	for _, ref := range d.Removed {
		sb.WriteString("- ")
		sb.WriteString(ref)
		sb.WriteString("\n")
	}
	for ref, entry := range d.Changed {
		sb.WriteString("~ ")
		sb.WriteString(ref)
		sb.WriteString(" ")
		sb.WriteString(entry.Before.Name)
		sb.WriteString(" → ")
		sb.WriteString(entry.After.Name)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
