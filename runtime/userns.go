package runtime

// UsernsSize is how many IDs the container's user namespace maps
// (docksmith run --userns-size). The default 1 maps only container root, to
// the invoking user. Above 1, container IDs 1..N-1 map to host IDs from
// UsernsHostBase+1, a range no host account uses; that needs a root
// invoker. It lets a service that refuses to run as root (PostgreSQL)
// switch to another user inside the container.
var UsernsSize = 1

// UsernsHostBase is the start of the host ID range for --userns-size.
const UsernsHostBase = 100000

// Opt-in container setup for workloads that need more than Docksmith's
// minimal sandbox (docksmith run --loopback, --dev, --user). All are off
// by default, so a plain run is
// unchanged. The child applies them before dropping its capabilities.
var (
	// ChildLoopback brings lo up in the container's network namespace.
	ChildLoopback bool
	// ChildDev bind-mounts the host's /dev/null, /dev/zero, /dev/full,
	// /dev/random, and /dev/urandom into the container's /dev.
	ChildDev bool
	// ChildUser, as "uid:gid", is the user the command runs as. IDs above
	// 0 need --userns-size larger than them.
	ChildUser string
)
