# Decision 3 — Omitted-key record syntax and its recovery

Serves R3, R3a, R4 of `docs/prds/PRD-oss-no-infisical.md`.

Question: what exact syntax does an omitted-key record take in each generated
environment-file format, and how does niwa's reader recover the key name and
declared description from the dotenv form?

## Context

### Verified: the writers

`internal/envformat/envformat.go` is a stdlib-only leaf package with one entry
point, `Marshal(format string, kvs []KV)` (envformat.go:36-47), dispatching to
three writers over the same ordered `[]KV`:

- `marshalDotenv` (envformat.go:52-62) — fixed header comment
  (`const header`, envformat.go:33) then raw `KEY=value\n` lines, **no quoting
  at all**.
- `marshalJSON` (envformat.go:68-91) — the constraint statement in the task
  brief needs correcting. It *is* hand-built (`buf.WriteString("{\n")`, manual
  commas) so member order is preserved rather than map-randomized, but each key
  and each value goes through `json.Marshal` individually (envformat.go:72-79),
  so escaping is correct for any byte. The relevant constraint is therefore not
  "escaping is unsafe" but "the object is **flat and string-valued**" — and
  `internal/envformat/envformat_test.go:23-42` pins that contract by decoding
  the output into `map[string]string`. A nested-object record would fail that
  existing test and break every consumer decoding into a string map.
- `marshalShell` (envformat.go:95-106) — the same header, then
  `export KEY='value'` with `'` → `'\''`.

### Verified: the reader

`parseEnvFile` (`internal/workspace/materialize.go:1438-1457`) is the whole
reader:

```go
for _, line := range strings.Split(string(data), "\n") {
    line = strings.TrimSpace(line)
    if line == "" || strings.HasPrefix(line, "#") { continue }
    key, value, ok := strings.Cut(line, "=")
    if !ok { continue }
    vars[key] = value
}
```

Four properties matter, all confirmed:

1. A line whose first non-space byte is `#` is dropped entirely
   (materialize.go:1447). It never reaches `Cut`, so a full-line comment cannot
   affect any other line's parse.
2. A line with no `=` is dropped silently (materialize.go:1451-1452). No error
   is ever returned for content; the only error path is `os.ReadFile`.
3. A line *with* an `=` becomes a map entry verbatim — no quote stripping, no
   `export ` handling. A trailing annotation on a `KEY=value` line would be
   swallowed into the value, as the brief states. Confirmed: there is no
   comment handling inside the value region here (unlike `.env.example`
   parsing, below).
4. Records are recovered in file order, which is sorted order, because the
   writer sorts (below). That gives R7 on the recovery path for free.

Three callers: `materialize.go:1169` (configured `env.files` inputs),
`materialize.go:1212` (discovered per-repo env file), and
`worktree_content.go:358` inside `readCloneEnvOutput`, which reads **every**
declared target through this dotenv parser regardless of the target's declared
format (`worktree_content.go:334-367`) — the format blindness R3a accepts. Note
what that already means for a `shell` target today: `export K='v'` parses to key
`"export K"`, value `"'v'"`. The blindness predates this work.

### Verified: sort and full rewrite

`EnvMaterializer.Materialize` builds the key list, `sort.Strings(keys)`
(materialize.go:1319-1323), converts to `[]envformat.KV`
(materialize.go:1324-1327), and for each target calls `envformat.Marshal` then
`os.WriteFile` (materialize.go:1361-1367) — unconditional, whole-file, every
apply. Nothing skips the write: the only fingerprint comparison in the apply
pipeline is `emitRotatedFiles` (`internal/workspace/apply.go:2162-2189`), which
only prints `rotated <path>`. **R4 costs nothing**: the record vanishes on the
next apply because the file is rebuilt from scratch. The worktree path is
equally safe — `inheritEnvOutputs` byte-copies the clone's file
(`worktree_content.go:302-314`), so records propagate and disappear with it.

### Verified: the description is hostile free text

Descriptions come from `EnvVarsTable.Required/Recommended/Optional`, all
`map[string]string` parsed from TOML (`internal/config/config.go:215-220`,
populated in `internal/config/env_tables.go:36-53`). TOML multi-line basic
strings mean a description can contain newlines, `=`, `#`, both quote
characters, and arbitrary Unicode. `internal/workspace/required.go:96-99`
already interpolates them raw into a terminal message.

A raw-interpolated description is therefore an **injection vector, not an edge
case**. A description of:

```
Ask a maintainer
PATH=/tmp/evil
```

interpolated into a dotenv comment produces a second physical line
`PATH=/tmp/evil` that `parseEnvFile` happily turns into a map entry. Every
candidate below is judged on whether it closes this.

### Verified: house precedent for the marker syntax

niwa already has a `# niwa: <payload>` marker convention in `.env.example`
parsing (`internal/workspace/env_example.go:32-37, 142-162`), with a key-name
charset `^[A-Za-z0-9_]+$` (env_example.go:20) and an explicit anti-spoofing
test suite (`internal/workspace/env_example_annotation_test.go:47-80`) asserting
that a `# niwa:` sequence inside a quoted value is not a marker. Any record
syntax chosen here should extend that prefix rather than invent a second one.

### One structural gap the record forces

`ResolveEnvVars` returns `nil, nil, nil` when the merged map is empty
(materialize.go:1230-1232) and `Materialize` returns early on
`len(vars) == 0` (materialize.go:1312-1314). A repo whose declared keys are
**all** unresolved therefore writes no file at all — and so carries no record,
failing R3 for exactly the first-run OSS contributor this PRD exists to serve.
Whichever syntax wins, both early returns must become "empty **and** no
records". This is a finding, not an option-discriminator: it applies equally to
all three candidates.

## Options

All examples assume two declared keys — `ANTHROPIC_API_KEY` (required,
provider unreachable) and `TAVILY_API_KEY` (optional, no provider configured) —
with `NIWA_PROFILE=dev` resolved, and a hostile description on the second key:

```
Search API key.
FOO=bar # "gotcha"
```

### Option A — per-key comment record, interleaved at the key's sorted position

Grammar, extending the existing `# niwa:` prefix:

```
# niwa: unresolved <KEY> <LEVEL> <REASON> <json-encoded-description>
```

`KEY` matches `^[A-Za-z0-9_]+$`; `LEVEL` ∈ {`required`, `recommended`,
`optional`, `undeclared`}; `REASON` ∈ {`unsatisfiable-declaration`,
`provider-unreachable`, `required-shortfall`} (the R5 taxonomy); the
description is `json.Marshal`'d, always quoted, always last.

**dotenv**

```
# Generated by niwa - do not edit manually
# niwa: unresolved ANTHROPIC_API_KEY required provider-unreachable "Anthropic API key used by the agent"
NIWA_PROFILE=dev
# niwa: unresolved TAVILY_API_KEY optional unsatisfiable-declaration "Search API key.\nFOO=bar # \"gotcha\""
```

**json** — the record is a string-valued member at the same sorted position, so
the object stays flat and `map[string]string`-decodable:

```json
{
  "//niwa:unresolved:ANTHROPIC_API_KEY": "required, provider-unreachable: Anthropic API key used by the agent",
  "NIWA_PROFILE": "dev",
  "//niwa:unresolved:TAVILY_API_KEY": "optional, unsatisfiable-declaration: Search API key.\nFOO=bar # \"gotcha\""
}
```

**shell** — byte-identical to the dotenv record (the two formats already share
`header`):

```sh
# Generated by niwa - do not edit manually
# niwa: unresolved ANTHROPIC_API_KEY required provider-unreachable "Anthropic API key used by the agent"
export NIWA_PROFILE='dev'
# niwa: unresolved TAVILY_API_KEY optional unsatisfiable-declaration "Search API key.\nFOO=bar # \"gotcha\""
```

Assessment:

- *Dotenv recoverability of name and description*: yes, with a reader change.
  `strings.CutPrefix(line, "# niwa: unresolved ")`, `SplitN(rest, " ", 4)`,
  validate key/level/reason against closed vocabularies, `json.Unmarshal` the
  fourth field. Exact round-trip of the description including newlines.
- *Hostile description*: closed. JSON encoding maps `\n` → `\n`, so a record is
  always exactly one physical line; `=`, `#`, and quotes live inside the
  comment and inside a quoted string. Spoofing a second record from inside a
  description is impossible for the same reason.
- *JSON validity*: preserved, and the flat string→string contract that
  `envformat_test.go:23-42` pins is preserved with it.
- *Shell sourceability*: preserved; `#` at line start is a POSIX comment and
  the line cannot contain a raw newline.
- *Legibility*: a reader sees the key where they would look for it, its
  declared level, why it is missing, and the author's description.
- *Round-trip through the existing reader*: `parseEnvFile` drops the line at
  materialize.go:1447 before `Cut` is ever reached, so no neighbouring line's
  bytes are touched — first line, last line, or between two assignments.
- *Cost*: `envformat.KV` grows record fields (or gains a sibling record type);
  `parseEnvFile` gains a records return.

### Option B — structured header block listing all omitted keys together

**dotenv / shell**

```
# Generated by niwa - do not edit manually
# niwa: unresolved-begin 2
# niwa:   ANTHROPIC_API_KEY required provider-unreachable "Anthropic API key used by the agent"
# niwa:   TAVILY_API_KEY optional unsatisfiable-declaration "Search API key.\nFOO=bar # \"gotcha\""
# niwa: unresolved-end
NIWA_PROFILE=dev
```

**json** — the block has no natural JSON rendering. Keeping the file flat and
string-valued forces the whole block into one member's value:

```json
{
  "//niwa:unresolved": "ANTHROPIC_API_KEY required provider-unreachable: Anthropic API key...; TAVILY_API_KEY optional ...",
  "NIWA_PROFILE": "dev"
}
```

Assessment: escaping and non-corruption are identical to Option A (same
prefix-skip, same JSON-encoded description). Its genuine advantage is the
first-run view — a contributor with eight unresolved keys sees all eight at the
top with a count, instead of hunting through the file. Its costs: a two-state
parser (begin/inner/end) whose failure modes include an unterminated block; the
record loses locality with the key; the JSON rendering degrades to one opaque
concatenated string that is neither legible nor per-key; and it needs its own
ordering rule and its own emit path rather than falling out of the single
sorted `[]KV` stream that already exists at materialize.go:1319-1327.

### Option C — sentinel assignment

```
ANTHROPIC_API_KEY__NIWA_UNRESOLVED=required provider-unreachable "Anthropic API key..."
NIWA_PROFILE=dev
```

Assessment: recoverable trivially and JSON-valid, but it fails R3's "the record
SHALL NOT carry a value" on its face, and it fails in three further ways.
`parseEnvFile` turns it into a real map entry (materialize.go:1450-1454), so
`readCloneEnvOutput` returns phantom variables to the settings materializer and
every downstream consumer of the map. In shell format it becomes a genuinely
exported environment variable whose value is the description. And the
description lands on an assignment line unquoted in dotenv, so a newline in it
injects a real `FOO=bar` assignment — the injection Option A closes.

### Option C2 — valueless sentinel word

```
NIWA_UNRESOLVED_ANTHROPIC_API_KEY
```

Assessment: carries no value and does not pollute the map (`Cut` fails,
materialize.go:1451). But it has no room for a description without inventing a
delimiter, and it breaks shell sourceability outright: a bare word is executed
as a command, producing `command not found` and a non-zero status that aborts
any `set -e` consumer.

### Option D — commented-out assignment

```
# ANTHROPIC_API_KEY=   # unresolved (required, provider-unreachable): Anthropic API key...
```

Assessment: the most dotenv-native-looking form, and safe against the existing
reader. Rejected on two grounds. Recovery means parsing structure inside a
comment across two `#` characters with no closed grammar, which is exactly the
fragile shape `env_example.go:170-200` had to write a quote-state machine to
survive. And it actively invites the user to uncomment the line — producing the
empty-value state R2 exists to forbid.

## Recommendation

**Option A**: a per-key `# niwa: unresolved` comment record, emitted at the
key's position in the existing sorted stream, with the description always
JSON-encoded and last.

Rationale:

1. It is the only candidate that satisfies R3's two halves without tension.
   Non-corruption is structural, not argued: the line is discarded at
   materialize.go:1447 before `strings.Cut` runs, so no adjacent assignment can
   change by a byte regardless of the record's content or position. Recovery is
   a closed grammar with one free-text field in terminal position.
2. It closes the hostile-description vector by construction. One physical line
   is an invariant of the encoding, not a property of the input.
3. It preserves both format contracts R3a's acceptance criteria name: the JSON
   file stays valid *and* stays the flat string→string object that
   `envformat_test.go:23-42` already pins; the shell file stays sourceable.
4. It reuses the sort that already exists. One rule — entries ordered by
   logical key name, each rendering as either an assignment or a record —
   gives R7 on both the write and the recovery path, and lets all three writers
   consume one `[]KV`-shaped stream.
5. It extends the `# niwa:` marker prefix the codebase already established
   (env_example.go:32-37), so there is one marker vocabulary rather than two.

Implementation shape:

- `envformat.KV` gains `Unresolved bool`, `Level`, `Reason`, `Description`
  (or the slice becomes `[]Entry` with an assignment/record discriminant).
  Each writer renders a record in its own format; with no records present all
  three writers emit byte-identical output to today, which is R19.
- `materialize.go:1312` and `materialize.go:1230` must stop early-returning on
  an empty value map when records exist (see Context, "one structural gap").
- `parseEnvFile` becomes a thin wrapper over
  `parseEnvFileWithRecords(path) (map[string]string, []unresolvedRecord, error)`.
  The two input-file callers (materialize.go:1169, 1212) keep the wrapper;
  `readCloneEnvOutput` (worktree_content.go:334) takes the records and threads
  them to the R6 report so the worktree path reports an inherited omission
  instead of failing a promoted-key lookup (R11).
- A malformed or unrecognised record line is skipped silently, matching the
  reader's existing tolerant contract (materialize.go:1451-1452).
- `json.Marshal` HTML-escapes `<`, `>`, `&` into `<`-style sequences. That
  is deterministic and reversible, so it is correct as-is; if a description
  containing those characters should stay readable, use a `json.Encoder` with
  `SetEscapeHTML(false)` and trim its trailing newline. Cosmetic, decide at
  implementation.

## Rejected alternatives

- **Header block (Option B)** — the closest contender. Better first-run
  overview, worse everything else: a stateful parser, lost locality, no honest
  JSON rendering, and a second ordering rule. If the overview turns out to
  matter, add a single summary line (`# niwa: 3 declared keys unresolved; see
  records below`) above the assignments; that buys the benefit without the
  block's parser.
- **Sentinel assignment (C)** — violates R3's no-value clause, pollutes the
  parsed map that `readCloneEnvOutput` feeds to settings promotion, exports a
  real shell variable, and reopens the description-injection vector.
- **Valueless sentinel word (C2)** — no room for a description; breaks
  `source` under `set -e`.
- **Commented-out assignment (D)** — fragile grammar inside a comment, and it
  invites a user edit that produces the empty value R2 forbids.
- **Raw (unencoded) description in any of the above** — a description with a
  newline injects an arbitrary assignment into every generated file for every
  repo. Not an edge case: descriptions are author-supplied TOML free text
  (config.go:215-220) that may come from a layer the reader cannot see.
- **Truncating long descriptions in the record** — would break R3's exact
  recovery. Left untruncated; see Open risks.

## Open risks

- **A secret value containing a newline can forge a record.** `marshalDotenv`
  writes values raw and unquoted (envformat.go:52-62); a value containing
  `\n# niwa: unresolved FAKE_KEY ...` produces a line the reader accepts. The
  blast radius is a misleading report entry — a forged record cannot set a
  value or hide a real one. Note that such a value *already* corrupts the file
  today (`envformat_test.go:55` documents this as a known dotenv limitation),
  so this work does not create the hazard. If cheap coverage is wanted, mirror
  the existing spoofed-marker test (env_example_annotation_test.go:47).
- **Unbounded description length.** A multi-kilobyte TOML description becomes a
  multi-kilobyte single comment line. Same exposure as the R6 terminal report,
  so no new policy is proposed here; if a cap is later wanted it belongs to
  both surfaces at once, not to the record alone.
- **Record fields beyond name and description are not required by R3.** Level
  and reason are included because the record is the only artifact a person
  reads inside the file, and they cost one closed-vocabulary token each. They
  widen the grammar slightly; if the reason taxonomy (R5) is renamed later, old
  records in existing files fail the vocabulary check and are skipped — benign,
  since the next apply rewrites them.
- **JSON and shell records remain unrecoverable by `readCloneEnvOutput`**
  (worktree_content.go:358 parses everything as dotenv). Accepted by R3a. One
  concrete consequence worth stating in the design: a repo whose only declared
  target is `json` gets no inherited-omission reporting on the worktree path —
  the record is present and legible in the file, but the reader cannot see it.
- **Two early returns must change together.** If only
  `Materialize`'s `len(vars) == 0` guard (materialize.go:1312) is fixed and
  `ResolveEnvVars`'s (materialize.go:1230) is not, an all-unresolved repo still
  writes nothing and R3 silently fails for the PRD's primary user.
