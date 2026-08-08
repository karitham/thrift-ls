// Every intentional mistake in the corpus, in one file. The comment
// above each section names the check that fires and the message to
// expect:
//
//   struct DuplicateFieldID    FieldIDCheck       — "field id conflict"
//   struct BadVault            FieldIDCheck       — "field id should be a
//                              positive integer in [1, 32767]"
//   struct Reg + enum Reg      DuplicateCheck      — "duplicate enum Reg"
//   struct DuplicateFieldName  DuplicateCheck      — "duplicate field
//                              same_name"
//   struct DoubleTrouble       FieldIDCheck + DuplicateCheck — two
//                              diagnostics on one line: "field id
//                              conflict" and "duplicate field repeat"
//   enum RelicKind             EnumValueCheck (warning) + the
//                              "Make enum values explicit" code action
//   enum CurseBreaker          DuplicateCheck      — "enum value 1
//                              duplicates OZEN"
//   enum DuplicateMember       DuplicateCheck      — "duplicate member
//                              RIKO"
//   service DescendUnion       DuplicateCheck      — "duplicate argument
//                              depth", "duplicate function descend"
//   struct Without             SemanticAnalysis — "field type doesn't
//                              exist"
//   const CURSED_LOCATIONS     DuplicateCheck      — "duplicate map key
//                              \"zone1\"", "duplicate set value 4"
//
// struct Clean at the bottom is fully valid: no diagnostics expected.

// FieldIDCheck — both `1:` tokens fire "field id conflict".
struct DuplicateFieldID {
  1: i32 weight,
  1: i32 range,
  2: string material,
}

// FieldIDCheck — ids must be in [1, 32767]: 0 and -1 are just as bad as
// 40000.
struct BadVault {
  40000: i32 too_high,
  0: i32 zero,
  -1: i32 negative,
}

// DuplicateCheck — the second Reg collides with the first: names share
// one top-level scope regardless of kind.
struct Reg {
  1: i32 range,
}

enum Reg {
  IDLE = 1,
}

// DuplicateCheck — both fields are named same_name.
struct DuplicateFieldName {
  1: i32 same_name,
  2: i32 same_name,
}

// Two checks on one line: the second field repeats both the id and the
// name of the first, so FieldIDCheck ("field id conflict") and
// DuplicateCheck ("duplicate field repeat") fire on the same line.
struct DoubleTrouble {
  1: i32 repeat,
  1: i32 repeat,
}

// EnumValueCheck — STAR_COMPASS, UNHEARD_BELL and CROSSED_STILLS have
// implicit values 0, 3 and 5. Putting the cursor anywhere on the enum
// offers "Make enum values explicit" as a refactor, and as a quickfix
// on the warnings.
enum RelicKind {
  STAR_COMPASS,
  BLAZE_REAP = 2,
  UNHEARD_BELL,
  GAVEL_OF_THE_ABYSS = 4,
  CROSSED_STILLS,
}

// DuplicateCheck — OZEN and MITTY both resolve to enum value 1.
enum CurseBreaker {
  OZEN = 1,
  MITTY = 1,
}

// DuplicateCheck — the third member repeats RIKO's name.
enum DuplicateMember {
  RIKO,
  NANACHI,
  RIKO,
}

// DuplicateCheck — argument depth twice in the first function, and the
// function name descend twice in the service.
service DescendUnion {
  void descend(1: i32 depth, 2: i32 depth),
  void descend(1: i32 far),
}

// SemanticAnalysis — MissingRelic and MissingLayer are not defined
// anywhere in the folder.
struct Without {
  1: MissingRelic missing,
  2: list<MissingLayer> xs,
}

// DuplicateCheck — the duplicated "zone1" key, and a set literal that
// contains 4 twice.
const map<string, set<i32>> CURSED_LOCATIONS = {
  "zone1": [1, 2, 3],
  "zone3": [3, 4, 4],
  "zone1": [7, 8, 9],
}

// Fully valid — no diagnostics expected here.
struct Clean {
  1: required i32 id,
  2: optional string name,
}
