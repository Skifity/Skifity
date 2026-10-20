#!/usr/bin/env bash
# A second server for the crosscheck, on the machine the first one runs on: a
# KVM guest on a bridge, booted from Ubuntu's own cloud image, that root can
# reach over SSH with a key made for it.
#
# The checks in verify.sh that need two servers are the ones a GitHub runner
# could never run: a runner is one machine, and nothing outside it can reach
# it. A runner can run a virtual machine, though, and a guest on a bridge is a
# second server on the same network as the first, which is the ordinary case
# for two servers at one provider. The panel then adds it the way it adds any
# server, and verify.sh is told where it is with VERIFY_SECOND_SERVER.
#
#   sudo bash test/cluster/second-server.sh /mnt/second
#
# It writes the guest's disk, its serial console and the key the panel signs
# in with (DIR/id_ed25519) into DIR, and prints the guest's address last.
# Needs /dev/kvm, qemu-system-x86_64, qemu-img and cloud-localds.
#
#   THIS CHANGES THE MACHINE IT RUNS ON: a bridge, a NAT rule and a route.
set -euo pipefail

DIR="${1:?give the directory to keep the guest disk and key in}"
BRIDGE="skifity-br0"
TAP="skifity-tap0"
NETWORK="192.168.100.0/24"
HOST_ADDRESS="192.168.100.1"
GUEST_ADDRESS="192.168.100.10"
GUEST_MAC="52:54:00:5c:1f:10"
: "${SECOND_SERVER_MEMORY_MB:=4096}"
: "${SECOND_SERVER_CPUS:=2}"
: "${SECOND_SERVER_DISK:=30G}"
: "${SECOND_SERVER_IMAGE:=https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img}"

say() { printf '%s\n' "$*" >&2; }
die() {
	say "second server: $*"
	exit 1
}

[ "$(id -u)" = "0" ] || die "this has to run as root: it makes a bridge and starts a virtual machine"
[ -e /dev/kvm ] || die "there is no /dev/kvm, so a guest would be emulated, far too slowly to install anything"
for tool in qemu-system-x86_64 qemu-img cloud-localds ssh ssh-keygen curl ip iptables; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed"
done

mkdir -p "$DIR"
KEY="$DIR/id_ed25519"

# --- the guest's disk and its first boot ------------------------------------

say "Downloading Ubuntu's cloud image"
curl -fsSL --retry 3 -o "$DIR/base.img" "$SECOND_SERVER_IMAGE" || die "the cloud image could not be downloaded"
qemu-img create -q -f qcow2 -F qcow2 -b "$DIR/base.img" "$DIR/disk.qcow2" "$SECOND_SERVER_DISK"

rm -f "$KEY" "$KEY.pub"
ssh-keygen -q -t ed25519 -N '' -C skifity-second-server -f "$KEY"

# Root signs in with the key and nothing else, as on a server somebody rents.
# The timers that run apt are switched off: a package upgrade holding the lock
# halfway through an install is a failure of the test, not of the product.
cat >"$DIR/user-data" <<EOF
#cloud-config
hostname: second
disable_root: false
ssh_pwauth: false
ssh_authorized_keys:
  - $(cat "$KEY.pub")
runcmd:
  - [systemctl, disable, --now, apt-daily.timer, apt-daily-upgrade.timer, unattended-upgrades.service]
EOF
printf 'instance-id: skifity-second\nlocal-hostname: second\n' >"$DIR/meta-data"
cat >"$DIR/network-config" <<EOF
version: 2
ethernets:
  primary:
    match:
      macaddress: "$GUEST_MAC"
    set-name: eth0
    addresses: [$GUEST_ADDRESS/24]
    routes:
      - to: default
        via: $HOST_ADDRESS
    nameservers:
      addresses: [1.1.1.1, 8.8.8.8]
EOF
cloud-localds --network-config="$DIR/network-config" "$DIR/seed.img" "$DIR/user-data" "$DIR/meta-data"

# --- the network between the two -------------------------------------------

# The address this machine is known by in the cluster: the one on its default
# route, which is what k3s takes as the node's address.
PRIMARY=$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')
[ -n "$PRIMARY" ] || die "could not tell which address this machine has on its default route"

ip link show "$BRIDGE" >/dev/null 2>&1 || ip link add "$BRIDGE" type bridge
ip addr replace "$HOST_ADDRESS/24" dev "$BRIDGE"
ip link set "$BRIDGE" up
ip link show "$TAP" >/dev/null 2>&1 || ip tuntap add dev "$TAP" mode tap
ip link set "$TAP" master "$BRIDGE"
ip link set "$TAP" up

# Out to the internet through this machine, for the guest's own downloads.
sysctl -q -w net.ipv4.ip_forward=1
iptables -t nat -C POSTROUTING -s "$NETWORK" ! -d "$NETWORK" -j MASQUERADE 2>/dev/null ||
	iptables -t nat -A POSTROUTING -s "$NETWORK" ! -d "$NETWORK" -j MASQUERADE
iptables -C FORWARD -i "$BRIDGE" -j ACCEPT 2>/dev/null || iptables -I FORWARD -i "$BRIDGE" -j ACCEPT
iptables -C FORWARD -o "$BRIDGE" -j ACCEPT 2>/dev/null || iptables -I FORWARD -o "$BRIDGE" -j ACCEPT

# What this machine sends to the guest comes from the address the cluster
# knows it by, not from the bridge's. The panel opens the guest's cluster ports
# to the other servers' addresses only, as it should, and two servers at a
# provider see each other's own addresses; without this the guest would see
# the bridge's and, rightly, refuse it.
ip route replace "$NETWORK" dev "$BRIDGE" src "$PRIMARY"

# --- boot -----------------------------------------------------------------------

say "Starting the guest: $SECOND_SERVER_CPUS CPUs, $SECOND_SERVER_MEMORY_MB MB"
qemu-system-x86_64 -enable-kvm -cpu host -smp "$SECOND_SERVER_CPUS" -m "$SECOND_SERVER_MEMORY_MB" \
	-drive "file=$DIR/disk.qcow2,if=virtio,format=qcow2" \
	-drive "file=$DIR/seed.img,if=virtio,format=raw" \
	-netdev "tap,id=net0,ifname=$TAP,script=no,downscript=no" \
	-device "virtio-net-pci,netdev=net0,mac=$GUEST_MAC" \
	-display none -serial "file:$DIR/serial.log" \
	-daemonize -pidfile "$DIR/qemu.pid"

SSH="ssh -i $KEY -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -o LogLevel=ERROR"
for _ in $(seq 1 60); do
	# shellcheck disable=SC2086 # SSH is several words on purpose.
	if $SSH "root@$GUEST_ADDRESS" 'cloud-init status --wait >/dev/null 2>&1; true' 2>/dev/null; then
		# shellcheck disable=SC2086
		ABOUT=$($SSH "root@$GUEST_ADDRESS" 'uname -sr; nproc; grep MemTotal /proc/meminfo' | tr -s ' \n' ' ')
		say "The guest is up: $ABOUT"
		printf '%s\n' "$GUEST_ADDRESS"
		exit 0
	fi
	sleep 5
done
tail -n 40 "$DIR/serial.log" >&2 || true
die "the guest did not answer on SSH within five minutes; its console is above"
