package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// handleRedelegate implements Issue 7 / #114: re-fire a previously-delegated
// task body without rewriting it. The handler reads the source envelope and
// state under a shared flock, validates that the caller is the source's
// kindDelegator, applies any body overrides, runs the same required_skills
// gate as handleDelegate, and creates a new task whose envelope carries
// `redelegated_from: <source_task_id>` for the audit chain.
//
// Source state may be any of the five legal states (queued, running,
// completed, abandoned, cancelled). The source's own state is unchanged —
// active sources keep running; the new task runs independently. Callers
// distinguish recovery flows from active forks via the `source_state_at_fork`
// field returned in the response.
//
// When source envelope.json is missing (the dominant taskstore_lost
// recreate-stub case from Issue 5), the handler returns SOURCE_BODY_LOST
// and asks the caller to supply the body via body_overrides. If the caller
// passes a body via body_overrides AND the source body is missing, the
// handler uses the override as the new body verbatim.
func (s *Server) handleRedelegate(args redelegateArgs) toolResult {
	s.maybeRegisterCoordinator()

	if args.SourceTaskID == "" {
		return errResult("source_task_id is required")
	}
	if args.Mode == "" {
		args.Mode = "async"
	}
	if args.Mode != "async" && args.Mode != "sync" {
		return errResultCode("BAD_PAYLOAD", "mode must be \"async\" or \"sync\"")
	}

	// Authorize: only the source's delegator may re-fire it. The auth path
	// is delicate for redelegate because the canonical recovery flows
	// (terminal source, taskstore_lost source with missing envelope.json)
	// trip authorizeTaskCall's defenses (TASK_ALREADY_TERMINAL,
	// NOT_TASK_PARTY) even when the caller is legitimately the source's
	// delegator. Pre-read state.json directly so we can authorize against
	// DelegatorRole when the envelope can't be read.
	taskDir := taskDirPath(s.taskStoreRoot(), args.SourceTaskID)
	srcState, stateErr := ReadStateOnly(taskDir)
	if stateErr != nil {
		// No state.json at all — the source task simply doesn't exist
		// from niwa's view. Mirror authorizeTaskCall's NOT_TASK_PARTY shape.
		return errResultCode("NOT_TASK_PARTY", "source task not found")
	}
	if srcState.DelegatorRole != "" && srcState.DelegatorRole != s.role {
		return errResultCode("NOT_TASK_OWNER",
			fmt.Sprintf("only the source task's delegator (%q) may redelegate", srcState.DelegatorRole))
	}
	// Read envelope.json best-effort; missing is the SOURCE_BODY_LOST case
	// surfaced below in mergeRedelegateBody.
	srcEnv, _ := readEnvelopeFile(taskDir)

	sourceState := "unknown"
	if srcState != nil {
		sourceState = srcState.State
	}

	// Compute the new body. Merge order: source body first, then overrides
	// at top level. If neither yields a body, return SOURCE_BODY_LOST.
	newBody, err := mergeRedelegateBody(srcEnv, args.BodyOverrides)
	if err != nil {
		return errResultCodeBody("SOURCE_BODY_LOST", map[string]any{
			"source_task_id": args.SourceTaskID,
			"detail":         err.Error(),
		})
	}

	// Compute target role and routing.
	to := args.To
	if to == "" && srcEnv != nil {
		to = srcEnv.To.Role
	}
	if to == "" {
		return errResult("could not determine target role: source.to.role missing and no `to` override provided")
	}
	sessionID := args.SessionID
	if sessionID == "" && srcState != nil {
		sessionID = srcState.SessionID
	}
	readOnly := false
	if args.ReadOnly != nil {
		readOnly = *args.ReadOnly
	}

	if !readOnly && sessionID == "" {
		return errResultCode("SESSION_REQUIRED",
			"redelegate without session_id requires read_only:true; pass either to route the new task")
	}
	if !s.isKnownRole(to) {
		return errResultCode("UNKNOWN_ROLE",
			fmt.Sprintf("role %q is not registered under .niwa/roles/", to))
	}
	if errTR := s.checkRequiredSkills(newBody); errTR.IsError {
		return errTR
	}

	taskID, errTR := s.createTaskEnvelope(to, newBody, args.ExpiresAt, "", sessionID, args.SourceTaskID)
	if errTR.IsError {
		return errTR
	}

	resp := map[string]any{
		"task_id":              taskID,
		"redelegated_from":     args.SourceTaskID,
		"source_state_at_fork": sourceState,
	}
	if args.Mode == "sync" {
		// Sync mode: block via the existing await path. The async response
		// shape is preserved as the immediate return so callers always see
		// task_id + audit chain even if they don't reach terminal state.
		await := s.handleAwaitTask(awaitTaskArgs{TaskID: taskID})
		// Augment the await response with the redelegate audit fields.
		// Format: structured-error responses pass through unchanged; the
		// happy-path response gets task_id and redelegated_from prepended.
		if !await.IsError {
			var awaitObj map[string]any
			if err := json.Unmarshal([]byte(await.Content[0].Text), &awaitObj); err == nil {
				awaitObj["task_id"] = taskID
				awaitObj["redelegated_from"] = args.SourceTaskID
				awaitObj["source_state_at_fork"] = sourceState
				if data, err := json.Marshal(awaitObj); err == nil {
					return textResult(string(data))
				}
			}
		}
		return await
	}
	data, _ := json.Marshal(resp)
	return textResult(string(data))
}

// mergeRedelegateBody computes the new body for a redelegate call. When
// the source envelope is present, its body is used as the base; overrides
// shallow-merge into the top level. When the source envelope is missing,
// overrides are required (else SOURCE_BODY_LOST) and they form the entire
// body.
func mergeRedelegateBody(srcEnv *TaskEnvelope, overrides map[string]json.RawMessage) (json.RawMessage, error) {
	if srcEnv == nil {
		if len(overrides) == 0 {
			return nil, errors.New("source envelope.json is missing; supply the new body via body_overrides")
		}
		// Build a new body from overrides only.
		data, err := json.Marshal(overrides)
		if err != nil {
			return nil, fmt.Errorf("marshal body_overrides: %w", err)
		}
		return data, nil
	}
	if len(overrides) == 0 {
		return srcEnv.Body, nil
	}
	// Shallow merge: read source body as a map, overlay overrides.
	var base map[string]json.RawMessage
	if err := json.Unmarshal(srcEnv.Body, &base); err != nil {
		// Source body isn't a JSON object — overrides take it whole.
		data, err := json.Marshal(overrides)
		if err != nil {
			return nil, fmt.Errorf("marshal body_overrides: %w", err)
		}
		return data, nil
	}
	if base == nil {
		base = map[string]json.RawMessage{}
	}
	for k, v := range overrides {
		base[k] = v
	}
	data, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshal merged body: %w", err)
	}
	return data, nil
}

// readEnvelopeFile reads envelope.json directly without acquiring the
// flock — used by the redelegate handler when authorizeTaskCall's
// terminal-state path has already prevented its own envelope read. Returns
// os.ErrNotExist when the envelope is missing (the taskstore_lost case).
func readEnvelopeFile(taskDir string) (*TaskEnvelope, error) {
	path := filepath.Join(taskDir, envelopeFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var env TaskEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, ErrCorruptedState
	}
	return &env, nil
}
