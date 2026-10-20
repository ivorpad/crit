package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// Codex's children of a pane (children.go): the sub-agent threads its lead
// thread spawned.

// codexChildren are the sub-agent threads the lead thread spawned, from Codex's
// thread index (thread_spawn_edges and each child's threads row) and the last
// task event in each child's rollout.
func codexChildren(ctx context.Context, run runner, thread string, now time.Time) ([]Child, error) {
	db := codexStateDB()
	if db == "" || !uuidRE.MatchString(thread) {
		return nil, nil
	}
	q := fmt.Sprintf(`select json_object('id',t.id,'rollout',t.rollout_path,'nick',coalesce(t.agent_nickname,''),`+
		`'path',coalesce(t.agent_path,''),'role',coalesce(t.agent_role,''),'model',coalesce(t.model,''),'created',t.created_at_ms)`+
		` from thread_spawn_edges e join threads t on t.id=e.child_thread_id where e.parent_thread_id='%s' order by t.created_at_ms;`, thread)
	out, err := run(ctx, nil, []string{"sqlite3", "-readonly", "-bail", "-batch", db, q})
	if err != nil {
		return nil, fmt.Errorf("reading Codex's thread index: %w", err)
	}
	var kids []Child
	for _, line := range strings.Split(string(out), "\n") {
		var row struct {
			ID, Rollout, Nick, Path, Role, Model string
			Created                              int64
		}
		if json.Unmarshal([]byte(line), &row) != nil || !uuidRE.MatchString(row.ID) {
			continue
		}
		ch := Child{ID: "cx:" + row.ID, Kind: "codex_subagent", Name: row.Nick, Type: row.Role, Task: row.Path, Model: row.Model,
			Status: "working", Reason: "running", ref: refCodexRollout, session: row.ID}
		if ch.Name == "" {
			ch.Name = path.Base(row.Path)
		}
		if row.Created > 0 {
			ch.Started = time.UnixMilli(row.Created).UTC().Format(time.RFC3339)
		}
		if mm := rolloutRE.FindStringSubmatch(row.Rollout); mm != nil && mm[1] == row.ID {
			ch.file = row.Rollout
		}
		switch rolloutEnd(ch.file) {
		case "task_complete":
			ch.Status, ch.Reason = "finished", "finished its task"
		case "turn_aborted":
			ch.Status, ch.Reason = "finished", "its turn was stopped"
		}
		kids = append(kids, observed(ch, now))
	}
	return kids, nil
}

// rolloutEnd is the last task event in the rollout's last 256 KB:
// task_started, task_complete or turn_aborted.
func rolloutEnd(p string) string {
	if p == "" {
		return ""
	}
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 256<<10 {
		_, _ = f.Seek(-256<<10, io.SeekEnd)
	}
	data, _ := io.ReadAll(io.LimitReader(f, 256<<10))
	last := ""
	for _, line := range bytes.Split(data, []byte("\n")) {
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "event_msg" {
			switch l.Payload.Type {
			case "task_started", "task_complete", "turn_aborted":
				last = l.Payload.Type
			}
		}
	}
	return last
}
