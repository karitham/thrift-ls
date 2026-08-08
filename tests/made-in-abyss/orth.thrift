// Include resolution: every type used below — Delver, Relic, WhistleRank,
// DiveTooDeep — must resolve through `include "abyss.thrift"`. Resolve any
// name and expect the definition in abyss.thrift; reference `delvers.thrift`
// and expect the same types while the file is open.
include "abyss.thrift"

namespace py orth

// Const referencing a builtin type: no resolution needed, just an int.
const i32 ORTH_DELIVER_COUNT = 64

// Typedef of a builtin: exercises the typedef node without pulling in
// another file.
typedef i32 relic_rarity

// Service using only included types, including a throws reference to the
// exception defined in the include.
service DelverUnion {
  void register_delver(1: Delver delver),
  void dive_relay(1: i32 depth) throws (1: DiveTooDeep too_deep),
  void ring_whistle(1: WhistleRank rank),
}