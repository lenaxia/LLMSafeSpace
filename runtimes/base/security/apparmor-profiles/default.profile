#include <tunables/global>

profile llmsafespace-default flags=(attach_disconnected) {
  #include <abstractions/base>
  #include <abstractions/nameservice>

  # Deny all by default
  deny /** rwx,

  # Allow read access to system files
  /usr/lib/** r,
  /lib/** r,
  /etc/ssl/** r,
  /etc/passwd r,
  /etc/group r,
  /etc/nsswitch.conf r,

  # Allow workspace access
  /workspace/** rw,
  /tmp/** rw,

  # Allow process operations
  capability chown,
  capability dac_override,
  capability setuid,
  capability setgid,

  # Network access
  network tcp,
  network udp,
}
