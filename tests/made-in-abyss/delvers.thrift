// Second consumer of abyss.thrift: same include, different set of
// cross-file references. Together with orth.thrift this exercises two
// files resolving the same include once the folder is open.
include "abyss.thrift"

namespace ts abyss

// Typedef and consts: trust the parser round-trip, no cross-file names.
typedef i32 lucerium_capacity

const i32 RICO_WHISTLE = 1

// Const struct with cross-file enum-qualified member references: the
// WhistleRank.RED / WhistleRank.WHITE dots resolve into abyss.thrift.
const Delver RIKO = {
  "name": "Riko",
  "whistle": WhistleRank.RED,
  "deepest_layer": 4,
}

const Delver REG = {
  "name": "Reg",
  "whistle": WhistleRank.WHITE,
  "deepest_layer": 5,
}

// Service overload-style: two functions over included types; the second
// throws into an included exception.
service DelverExpedition {
  void descend(1: Delver team, 2: optional i32 max_depth),
  Relic salvage(1: string layer, 2: map<string, i32> loot) throws (1: DiveTooDeep too_deep),
}