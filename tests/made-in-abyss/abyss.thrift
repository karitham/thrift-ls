// Core types of the made-in-abyss corpus. This file has no dependencies:
// it exists so the other files can exercise include resolution and
// cross-file references against it, with every name and value deliberate
// and no diagnostics expected here.
namespace go abyss

// Enum with explicit values: no EnumValueCheck warnings, no duplicate
// values. Keep it that way; the implicit-value demo lives in lints.thrift.
enum Layer {
  EDGE = 1,
  FOREST_OF_TEMPTATION = 2,
  THE_GREAT_FAULT = 3,
  GOBLETS_OF_GIANTS = 4,
  SEA_OF_CORPSES = 5,
  CAPITAL_OF_THE_UNRETURNED = 6,
  FINAL_MAELSTROM = 7,
}

enum WhistleRank {
  RED = 1,
  BLUE = 2,
  MOON = 3,
  BLACK = 4,
  WHITE = 5,
}

enum CurseSeverity {
  LIGHT = 1,
  MODERATE = 2,
  SEVERE = 3,
  FATAL = 4,
}

// Field ids 1-2, explicit qualifiers on 2: exercises required/optional
// parsing without triggering any lint.
struct Delver {
  1: string name,
  2: required WhistleRank whistle,
  3: i32 deepest_layer,
  4: optional list<string> relic_names,
}

struct Relic {
  1: string name,
  2: optional string curse_note,
  3: optional i32 lucerium_value,
}

struct Hollow {
  1: string name,
  2: bool is_hollowed,
}

// Exception referenced by orth.thrift's throws clause: cross-file
// resolution of named types.
exception DiveTooDeep {
  1: i32 requested_layer,
  2: string message,
}
