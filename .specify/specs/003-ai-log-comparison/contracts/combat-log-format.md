# Contract: Combat log input and what the parser keeps

**Feature**: `003-ai-log-comparison`
**Package**: `internal/combatlog` (pure; the parser worker in the app feeds it a
stream and stores what comes out)
**Source of format facts**: research.md D1 (format version 22, patch 12.0)

## Input

A gzip stream of one or more concatenated members (the uploader's pieces), decoding
to the text of `WoWCombatLog.txt`. The parser reads it once, forwards only, in
`bufio.Scanner` lines with a 1 MiB line cap (`COMBATANT_INFO` lines run to tens of
kilobytes).

### Accepted line shapes

```
9/14/2026 20:15:32.123-4  ENCOUNTER_START,3009,"Vexie and the Geargrinders",16,20,2769
9/14 20:15:32.123  ENCOUNTER_START,...                      (older client: no year, no offset)
COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,1,BUILD_VERSION,12.0.1,PROJECT_ID,1
```

- Timestamp then two spaces (or a tab), then a CSV record. The offset, when present,
  is hours (`-4`) or `±HH:MM`; when absent the guild's zone is assumed. A year, when
  absent, is the year the upload was made unless that puts the first line in the
  future, in which case the year before.
- The CSV record is read with `encoding/csv` in lazy-quotes mode; fields inside
  `[…]` and `(…)` are re-tokenised by the bracket reader.
- A line that does not parse is counted and skipped; parsing fails only when the
  first non-empty line is not a `COMBAT_LOG_VERSION` header (spec: "not a combat
  log") or when more than 1% of lines are unreadable (a file in some other format).

### Events the parser acts on

| Event | Fields used | Effect |
|---|---|---|
| `COMBAT_LOG_VERSION` | version, `ADVANCED_LOG_ENABLED` | Recorded on the upload; decides whether the advanced block is expected |
| `ENCOUNTER_START` | encounterID, name, difficultyID, groupSize | Opens a fight; resets per-character totals |
| `COMBATANT_INFO` | GUID, specID, talents `[…]`, gear `[…]` | Kept only when the GUID belongs to one of the member's characters (see matching) |
| `SWING_DAMAGE`, `SPELL_DAMAGE`, `SPELL_PERIODIC_DAMAGE`, `RANGE_DAMAGE`, `DAMAGE_SPLIT` | source, advanced block (ownerGUID), suffix amount/overkill | Adds effective damage to the source, or to the owner when the source is a pet with a known owner |
| `SPELL_HEAL`, `SPELL_PERIODIC_HEAL` | source, suffix amount/overheal | Adds `amount − overheal` to the source |
| `SPELL_CAST_SUCCESS` | source, spellID, spellName, timestamp | Increments the cast count; records the offset from pull while count < 10 |
| `UNIT_DIED` | dest | Adds a death to a member character |
| `SPELL_SUMMON` | source, dest | Learns pet → owner for pets without an advanced block |
| `ENCOUNTER_END` | success, fightTime | Closes the fight as kill or wipe; emits it |
| anything else | — | Skipped without parsing beyond the event name |

Within a fight, lines are attributed by source GUID; GUIDs of the member's characters
are learned from the first line in which the name matches (D3) and remembered for the
file. Damage from a pet whose owner is unknown when the line is read is held per pet
until the owner is learned from any later advanced block or summon, then attributed;
still unknown at `ENCOUNTER_END`, it is dropped and counted in the parse statistics.

### Matching the member's characters

Input to the parser: the uploading member's account characters as
`(name, realmSlug)` pairs. A log name matches when `lower(name)` is equal and
`squash(realm)` equals `squash(realmSlug)`, where `squash` lowercases and removes
everything that is not a letter or digit. Only matched characters produce summaries;
every other player's lines are discarded as read. No other player's name, GUID or
number is retained anywhere (SC-008).

## Output

Per encounter, in file order:

```go
type Fight struct {
    EncounterID, DifficultyID, GroupSize int
    Name       string
    Kill       bool
    StartedAt  time.Time
    Duration   time.Duration
    Summaries  []Summary       // one per matched character seen in the fight
}

type Summary struct {
    Name, RealmSlug string
    SpecID          int          // 0 without advanced logging
    Damage, Healing int64
    Deaths          int
    Active          time.Duration
    Casts           []Cast       // {ID, Name, Count, At []float64}
    Talents         []Talent     // {Node, Entry, Rank}; nil without COMBATANT_INFO
    Gear            []GearPiece  // {Slot, Item, Level, Enchant, Bonus []int, Gems []int}; nil likewise
}
```

Plus a `Stats` value for the whole file: lines read, lines skipped, fights, pets
unattributed, advanced logging on/off, log version — logged and used for the audit
detail.

## Guarantees

- **Streaming**: memory is bounded by one encounter's per-character totals and the
  pending-pet buffer; the file's size does not matter.
- **Deterministic**: the same input produces the same output.
- **No I/O**: the package reads an `io.Reader` and returns values; it opens no file,
  touches no database, makes no request.
- **Order**: fights are emitted in file order, `ordinal` 1-based.

## Test obligations

Table-driven, in `internal/combatlog`:

- both timestamp shapes; year inference; offset forms;
- each acted-on event with and without the advanced block, including `SWING_DAMAGE`'s
  shorter layout;
- overkill −1 and positive; overheal subtraction; absorbed not double-counted;
- pet attribution via advanced owner, via summon, pre-existing pet buffered then
  attributed, never attributed dropped;
- `COMBATANT_INFO` tokenizer: talents, PvP tuple, gear with nested tuples, auras;
  specID located by position before the first `[`;
- name matching table (spaces, apostrophes, accents, connected-realm names);
- an encounter with no end; a header-less file refused; a >1%-garbage file refused;
- a synthetic 300-line "night" fixture producing three fights with known numbers.
