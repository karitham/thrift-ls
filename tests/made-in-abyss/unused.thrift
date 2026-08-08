// A valid file that nothing includes for real: lints.thrift includes it
// only so UnusedIncludeCheck has something to flag, and nothing in the
// corpus references a type from here.
namespace go unused

struct Spare {
  1: i32 id,
}
