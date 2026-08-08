// CycleCheck — cycle_a.thrift and cycle_b.thrift include each other.
// Each file uses a type from the other, so the includes are used and only
// the cycle diagnostics fire.
// Expect the cycle diagnostic on this include while the folder is open.
include "cycle_b.thrift"

namespace go made

struct FromCycleA {
  1: FromCycleB other,
}