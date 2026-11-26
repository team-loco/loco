package kindnode

// PrepareUserNamespaceSysfs gives nested pod user namespaces an unmasked sysfs.
// Kind masks DMI files under /sys, preventing sandbox sysfs mounts on Linux CI.
// See https://github.com/kubernetes-sigs/kind/issues/3436.
const PrepareUserNamespaceSysfs = `
userns_sysfs=/run/loco-userns-sysfs
mkdir -p "$userns_sysfs"
if ! mountpoint -q "$userns_sysfs"; then
    mount -t sysfs -o ro,nosuid,nodev,noexec sysfs "$userns_sysfs"
fi`
