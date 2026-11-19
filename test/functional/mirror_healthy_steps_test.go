package functional

// Steps for the healthy-check scenario in features/mirror-repair.feature. The
// check's cost is shown in the unit tests by counting the git processes it
// starts; here the scenario shows it needs no network, and that each check
// line records how long it took without asserting any bound on it, since a
// wall-clock threshold would make the scenario depend on the host's load.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/cucumber/godog"
)

func registerMirrorHealthySteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^every check line the mirror log gained carries check_ms as a whole number of 0 or more$`, everyCheckLineCarriesCheckMs)
}

func everyCheckLineCarriesCheckMs(ctx context.Context) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	checks := 0
	for _, line := range lines {
		if line["event"] != "check" {
			continue
		}
		checks++
		raw, err := json.Marshal(line["check_ms"])
		if err != nil {
			return err
		}
		ms, ok := line["check_ms"].(float64)
		if !ok || ms < 0 || ms != math.Trunc(ms) {
			return fmt.Errorf("check line %v carries check_ms %s, want a whole number of 0 or more", line, raw)
		}
	}
	if checks == 0 {
		return fmt.Errorf("the mirror log gained no check line: %v", lines)
	}
	return nil
}
