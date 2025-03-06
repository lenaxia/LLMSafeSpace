#include <tunables/global>

profile llmsafespace-high-security flags=(attach_disconnected) {
    #include <abstractions/base>
    #include <abstractions/nameservice>

    # Completely deny network access
    deny network,

    # Deny all filesystem access by default
    deny /** rwx,

    # Allow read access to system files
    /usr/lib/** r,
    /lib/** r,
    /etc/ssl/** r,
    /etc/passwd r,
    /etc/group r,

    # Restricted workspace access
    /workspace/** rw,
    /tmp/** rw,

    # Minimal process capabilities
    capability chown,
    capability setuid,
    capability setgid,

    # Deny dangerous operations
    deny /proc/** rwx,
    deny /sys/** rwx,
    deny /dev/** rwx,
    deny /boot/** rwx,
}
