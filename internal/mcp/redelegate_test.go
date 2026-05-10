package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleRedelegate_FromAbandoned verifies the canonical recovery path:
// redelegate from an abandoned source produces a new task with the same
// body, attribution reset to the caller, redelegated_from chained to the
// source, and source_state_at_fork in the response.
func TestHandleRedelegate_FromAbandoned(t *testing.T) {
	s := newTestServer(t, "coordinator", "web")
	srcID := writeTaskFixture(t, s.instanceRoot, "coordinator", "web", TaskStateAbandoned)

	res := s.handleRedelegate(redelegateArgs{
		SourceTaskID: srcID,
		To:           "web",
		ReadOnly:     ptrBool(true),
	})
	if res.IsError {
		t.Fatalf("redelegate from abandoned: %s", res.Content[0].Text)
	}
	var resp map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &resp)
	newTaskID, _ := resp["task_id"].(string)
	if newTaskID == srcID || newTaskID == "" {
		t.Errorf("task_id = %q; must be a fresh ID, not source", newTaskID)
	}
	if resp["redelegated_from"] != srcID {
		t.Errorf("redelegated_from = %v, want %s", resp["redelegated_from"], srcID)
	}
	if resp["source_state_at_fork"] != TaskStateAbandoned {
		t.Errorf("source_state_at_fork = %v, want abandoned", resp["source_state_at_fork"])
	}

	// Verify the new envelope's RedelegatedFrom field is set.
	envBytes, err := os.ReadFile(filepath.Join(s.instanceRoot, ".niwa", "tasks", newTaskID, envelopeFileName))
	if err != nil {
		t.Fatalf("read new envelope: %v", err)
	}
	var newEnv TaskEnvelope
	_ = json.Unmarshal(envBytes, &newEnv)
	if newEnv.RedelegatedFrom != srcID {
		t.Errorf("new envelope RedelegatedFrom = %q, want %q", newEnv.RedelegatedFrom, srcID)
	}
	if newEnv.From.Role != "coordinator" {
		t.Errorf("new envelope From.Role = %q, want coordinator (caller)", newEnv.From.Role)
	}
}

// TestHandleRedelegate_FromRunning_ActiveFork verifies that redelegating
// from a running source succeeds, the source's state is unchanged, and
// the response surfaces source_state_at_fork="running" so the caller
// knows they just forked active work.
func TestHandleRedelegate_FromRunning_ActiveFork(t *testing.T) {
	s := newTestServer(t, "coordinator", "web")
	srcID := writeTaskFixture(t, s.instanceRoot, "coordinator", "web", TaskStateRunning)

	res := s.handleRedelegate(redelegateArgs{
		SourceTaskID: srcID,
		To:           "web",
		ReadOnly:     ptrBool(true),
	})
	if res.IsError {
		t.Fatalf("redelegate from running: %s", res.Content[0].Text)
	}
	var resp map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &resp)
	if resp["source_state_at_fork"] != TaskStateRunning {
		t.Errorf("source_state_at_fork = %v, want running", resp["source_state_at_fork"])
	}

	// Source's state.json must be unchanged (still running).
	srcDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", srcID)
	srcSt, err := ReadStateOnly(srcDir)
	if err != nil {
		t.Fatalf("ReadStateOnly source: %v", err)
	}
	if srcSt.State != TaskStateRunning {
		t.Errorf("source state mutated; got %q want running", srcSt.State)
	}
}

// TestHandleRedelegate_SourceBodyLost verifies SOURCE_BODY_LOST when the
// source envelope.json is missing — the dominant taskstore_lost recreate-
// stub case from Issue 5. The caller can recover by passing body_overrides.
func TestHandleRedelegate_SourceBodyLost(t *testing.T) {
	s := newTestServer(t, "coordinator", "web")
	srcID := writeTaskFixture(t, s.instanceRoot, "coordinator", "web", TaskStateAbandoned)
	// Delete the envelope to simulate the taskstore_lost recreate-stub.
	srcDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", srcID)
	if err := os.Remove(filepath.Join(srcDir, envelopeFileName)); err != nil {
		t.Fatal(err)
	}

	res := s.handleRedelegate(redelegateArgs{
		SourceTaskID: srcID,
		To:           "web",
		ReadOnly:     ptrBool(true),
	})
	if !res.IsError {
		t.Fatalf("expected SOURCE_BODY_LOST when envelope missing; got success: %s", res.Content[0].Text)
	}
	if errorCode(&res) != "SOURCE_BODY_LOST" {
		t.Errorf("error code = %q, want SOURCE_BODY_LOST", errorCode(&res))
	}

	// Recovery via body_overrides — caller supplies the body explicitly.
	res = s.handleRedelegate(redelegateArgs{
		SourceTaskID: srcID,
		To:           "web",
		ReadOnly:     ptrBool(true),
		BodyOverrides: map[string]json.RawMessage{
			"kind": json.RawMessage(`"recovery"`),
		},
	})
	if res.IsError {
		t.Errorf("redelegate with body_overrides should succeed; got: %s", res.Content[0].Text)
	}
}

// TestHandleRedelegate_BodyOverridesMerge verifies that body_overrides
// shallow-merge into the source body at top level — overlapping keys win,
// non-overlapping keys preserve.
func TestHandleRedelegate_BodyOverridesMerge(t *testing.T) {
	s := newTestServer(t, "coordinator", "web")
	srcID := writeTaskFixture(t, s.instanceRoot, "coordinator", "web", TaskStateCompleted)
	// Replace the source envelope's body with a structured object.
	srcDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", srcID)
	envBytes, _ := os.ReadFile(filepath.Join(srcDir, envelopeFileName))
	var env TaskEnvelope
	_ = json.Unmarshal(envBytes, &env)
	env.Body = json.RawMessage(`{"kind":"original","level":1,"keep":true}`)
	envBytes, _ = json.Marshal(env)
	_ = os.WriteFile(filepath.Join(srcDir, envelopeFileName), envBytes, 0o600)

	res := s.handleRedelegate(redelegateArgs{
		SourceTaskID: srcID,
		To:           "web",
		ReadOnly:     ptrBool(true),
		BodyOverrides: map[string]json.RawMessage{
			"kind":  json.RawMessage(`"override"`),
			"level": json.RawMessage(`2`),
		},
	})
	if res.IsError {
		t.Fatalf("redelegate: %s", res.Content[0].Text)
	}
	var resp map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &resp)
	newTaskID := resp["task_id"].(string)

	newEnvBytes, _ := os.ReadFile(filepath.Join(srcDir, "..", newTaskID, envelopeFileName))
	var newEnv TaskEnvelope
	_ = json.Unmarshal(newEnvBytes, &newEnv)
	var body map[string]json.RawMessage
	_ = json.Unmarshal(newEnv.Body, &body)
	if string(body["kind"]) != `"override"` {
		t.Errorf("kind = %s, want \"override\" (override wins)", body["kind"])
	}
	if string(body["level"]) != `2` {
		t.Errorf("level = %s, want 2 (override wins)", body["level"])
	}
	if string(body["keep"]) != `true` {
		t.Errorf("keep = %s, want true (preserved from source)", body["keep"])
	}
}

// TestHandleRedelegate_NotOwner verifies that only the source's delegator
// can redelegate.
func TestHandleRedelegate_NotOwner(t *testing.T) {
	s := newTestServer(t, "web", "web") // s.role = web, but source delegator = coordinator
	srcID := writeTaskFixture(t, s.instanceRoot, "coordinator", "web", TaskStateAbandoned)

	res := s.handleRedelegate(redelegateArgs{
		SourceTaskID: srcID,
		To:           "web",
		ReadOnly:     ptrBool(true),
	})
	if !res.IsError {
		t.Errorf("expected error when caller is not the source's delegator")
	}
	if errorCode(&res) != "NOT_TASK_OWNER" {
		t.Errorf("error code = %q, want NOT_TASK_OWNER", errorCode(&res))
	}
}

// TestHandleRedelegate_MissingSkillsGate verifies that the
// required_skills gate fires on redelegate against the merged body.
func TestHandleRedelegate_MissingSkillsGate(t *testing.T) {
	s := newTestServer(t, "coordinator", "web")
	// Empty manifest — no skills installed, so any required_skills entry
	// triggers MISSING_SKILLS.
	srcID := writeTaskFixture(t, s.instanceRoot, "coordinator", "web", TaskStateAbandoned)
	srcDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", srcID)
	envBytes, _ := os.ReadFile(filepath.Join(srcDir, envelopeFileName))
	var env TaskEnvelope
	_ = json.Unmarshal(envBytes, &env)
	env.Body = json.RawMessage(`{"required_skills":["nonexistent:skill"]}`)
	envBytes, _ = json.Marshal(env)
	_ = os.WriteFile(filepath.Join(srcDir, envelopeFileName), envBytes, 0o600)

	res := s.handleRedelegate(redelegateArgs{
		SourceTaskID: srcID,
		To:           "web",
		ReadOnly:     ptrBool(true),
	})
	if !res.IsError {
		t.Errorf("expected MISSING_SKILLS to propagate through redelegate")
	}
	if errorCode(&res) != "MISSING_SKILLS" {
		t.Errorf("error code = %q, want MISSING_SKILLS", errorCode(&res))
	}
	if !strings.Contains(res.Content[0].Text, "nonexistent:skill") {
		t.Errorf("error body should name the missing skill; got %s", res.Content[0].Text)
	}
}

func ptrBool(b bool) *bool { return &b }
