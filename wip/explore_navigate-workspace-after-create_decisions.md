# Exploration Decisions: navigate-workspace-after-create

## Round 1
- Niwa should own shell integration, not tsuku: all six research leads converge on this. Tsuku has no post-install shell mechanism, and adding one costs 200+ LOC across two repos with only one consumer.
- Completions don't validate tsuku generalization: cobra provides per-tool completion generation for free, removing the second use case that motivated the tsuku angle.
- The eval-init pattern is the right approach: proven by zoxide/direnv/mise, lets the binary version its shell output, handles fish naturally, and is a known convention users understand.
- Demand is legitimate but early-stage: maintainer-filed, explicitly deferred in design doc, no external validation yet. Proceed with design work but don't treat as urgent.
