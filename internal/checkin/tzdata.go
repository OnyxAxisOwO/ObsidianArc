package checkin

// Windows ships no zone database, and the copy under GOROOT is missing on a
// host that runs the binary without a Go toolchain. There LoadLocation fails,
// and the check-in and the bonus pools, which take their days from Location,
// count them in UTC; the warning in Location is then the only sign. Embedding
// the database costs about 400 KB of binary and removes the dependence on the
// host.
import _ "time/tzdata"
