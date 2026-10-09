#!/bin/sh
# Trixie package-verification spike (spec acceptance work; findings: build/PACKAGE-VERIFY.md).
# Runs INSIDE the pinned build container before every ISO build and fails the build if a
# wave-1 required package becomes unresolvable at the pinned snapshot.
# Usage: package-verify.sh <output-file> <mirror-base-url>
set -eu

OUT="${1:?usage: package-verify.sh <output-file> <mirror>}"
MIRROR="${2:?usage: package-verify.sh <output-file> <mirror>}"

SOURCES=/etc/apt/sources.list.d/axonwall-verify.list
trap 'rm -f "$SOURCES"' EXIT
cat > "$SOURCES" <<EOF
deb ${MIRROR} trixie main non-free-firmware
EOF

apt-get update -qq

# verify <package> <required|informational>
# Writes findings to $OUT as it goes; a missing required package fails the build.
verify() {
	_pkg="$1"
	_level="$2"
	_cand="$(apt-cache policy "${_pkg}" | sed -n 's/^  Candidate: //p')"
	if [ -n "${_cand}" ] && [ "${_cand}" != "(none)" ]; then
		echo "PASS  ${_pkg}  ${_cand}  (${_level})" >> "${OUT}"
	else
		echo "MISS  ${_pkg}  -  (${_level})" >> "${OUT}"
		if [ "${_level}" = "required" ]; then
			echo "FATAL: required package unresolvable at the pinned mirror: ${_pkg}" >&2
			exit 1
		fi
	fi
}

{
	echo "# AxonWall trixie package verification"
	echo "# date (UTC): $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
	echo "# mirror: ${MIRROR}"
	echo "# method: apt-cache policy inside the pinned build container"
	echo
	echo "## wave-1 runtime (required)"
} > "${OUT}"

verify dnsmasq required
verify unbound required
verify wireguard-tools required
verify nftables required
verify openssh-server required
verify systemd required

{
	echo
	echo "## wave-2 targets (informational)"
} >> "${OUT}"
verify kea informational
verify kea-dhcp4-server informational

{
	echo
	echo "## curated firmware subset (required)"
} >> "${OUT}"
verify firmware-realtek required
verify firmware-iwlwifi required
verify firmware-misc-nonfree required

{
	echo
	echo "## Secure Boot chain (required)"
} >> "${OUT}"
verify shim-signed required
verify grub-efi-amd64-signed required

{
	echo
	echo "## captive-portal daemon re-check (expect CONFIRMED-ABSENT)"
} >> "${OUT}"
_found="$(apt-cache search --names-only '^(wifidog|coova-chilli|chillispot)$' || true)"
# opennds IS packaged in trixie (10.3.1+dfsg-1) — see build/PACKAGE-VERIFY.md; a
# re-appearance here is informational and does not change wave-1 scope.
_opennds="$(apt-cache policy opennds | sed -n 's/^  Candidate: //p')"
if [ -n "${_found}" ]; then
	echo "PRESENT: ${_found}" >> "${OUT}"
else
	echo "CONFIRMED-ABSENT: wifidog, coova-chilli, chillispot" >> "${OUT}"
fi
echo "opennds candidate: ${_opennds:-(none)}" >> "${OUT}"

cat "${OUT}"
