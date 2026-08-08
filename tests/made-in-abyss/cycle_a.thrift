// CycleCheck — cycle_a.thrift and cycle_b.thrift include each other.
// Expect the cycle diagnostic on this include while the folder is open.
include "cycle_b.thrift"

namespace go made

struct FromCycleA {}