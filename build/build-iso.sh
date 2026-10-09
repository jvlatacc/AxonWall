#!/usr/bin/env bash
# AxonWall ISO factory — ONE command from a clean checkout (spec acceptance criterion 1).
#
# Intended to run as root INSIDE the pinned build container (build/container/Dockerfile):
#   docker build -t axonwall/iso-builder build/container
#   docker run --rm --privileged -v "$PWD:/repo" axonwall/iso-builder build/build-iso.sh
#
# Steps: package verification -> live-build config -> lb build -> verify media
# (BIOS + UEFI + Secure Boot chain) -> 2 GiB size guard -> dist/ outputs with
# SHA256SUMS + package manifest.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(git -C "${SCRIPT_DIR}/.." rev-parse --show-toplevel 2>/dev/null || cd "${SCRIPT_DIR}/.." && pwd)"
LB_DIR="${REPO_ROOT}/build/livebuild"
DIST_DIR="${REPO_ROOT}/dist"

SNAPSHOT="${AXONWALL_SNAPSHOT:-20261005T000000Z}"
case "${AXONWALL_MIRROR_MODE:-snapshot}" in
	snapshot)
		MIRROR_BUILD="https://snapshot.debian.org/archive/debian/${SNAPSHOT}"
		;;
	live)
		MIRROR_BUILD="https://deb.debian.org/debian"
		;;
	*)
		echo "FATAL: AXONWALL_MIRROR_MODE must be 'snapshot' or 'live'" >&2
		exit 2
		;;
esac
export AXONWALL_SNAPSHOT AXONWALL_MIRROR_MODE

if [ "$(id -u)" -ne 0 ]; then
	echo "FATAL: live-build needs root — run build-iso.sh inside the pinned container" >&2
	exit 1
fi

mkdir -p "${DIST_DIR}"

echo "== [1/4] trixie package verification (mirror: ${MIRROR_BUILD})"
"${SCRIPT_DIR}/package-verify.sh" "${DIST_DIR}/package-verify.txt" "${MIRROR_BUILD}"

echo "== [2/4] live-build configuration"
cd "${LB_DIR}"
./auto/config
lb clean >/dev/null 2>&1 || true

echo "== [3/4] lb build (debootstrap -> install -> squashfs -> ISO)"
lb build

echo "== [4/4] verify media and collect outputs"
ISO_SRC="${LB_DIR}/live-image-amd64.hybrid.iso"
[ -f "${ISO_SRC}" ] || ISO_SRC="${LB_DIR}/binary.iso"
[ -f "${ISO_SRC}" ] || {
	echo "FATAL: live-build produced no ISO (looked for live-image-amd64.hybrid.iso and binary.iso under ${LB_DIR})" >&2
	exit 1
}

VERSION="$(git -C "${REPO_ROOT}" describe --tags --always --dirty 2>/dev/null || echo dev)"
ISO_NAME="axonwall-${VERSION}-amd64.iso"
mv "${ISO_SRC}" "${DIST_DIR}/${ISO_NAME}"

# Package manifest: the complete installed set, straight from the build chroot's dpkg db.
chroot "${LB_DIR}/chroot" dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n' \
	| LC_ALL=C sort > "${DIST_DIR}/${ISO_NAME}.manifest.txt"

cd "${DIST_DIR}"
sha256sum "${ISO_NAME}" "${ISO_NAME}.manifest.txt" > SHA256SUMS

# Media verification: dual El Torito boot entries (BIOS + UEFI) and the Secure Boot
# chain inside the ESP image (shim -> signed grub).
REPORT="$(xorriso -indev "${ISO_NAME}" -report_el_torito plain 2>/dev/null)" || {
	echo "FATAL: xorriso could not read the ISO" >&2
	exit 1
}
printf '%s\n' "${REPORT}" > "${DIST_DIR}/el-torito-report.txt"
echo "${REPORT}" | grep -qi 'isolinux' || {
	echo "FATAL: no BIOS (isolinux) El Torito boot entry — hybrid media broken" >&2
	exit 1
}
# xorriso's plain report splits per-image fields across lines:
#   El Torito boot img :   2  UEFI  y   none  0x0000  0x00   6656  135
#   El Torito img path :   2  /boot/grub/efi.img
# Join them by image number (field 6) instead of assuming a one-line format.
UEFI_N="$(printf '%s\n' "${REPORT}" | awk '$1=="El" && $3=="boot" && $4=="img" && $7=="UEFI" {print $6; exit}')"
if [ -z "${UEFI_N}" ]; then
	echo "FATAL: no UEFI boot image in the El Torito report; report follows:" >&2
	cat "${DIST_DIR}/el-torito-report.txt" >&2
	exit 1
fi
EFI_IMG_PATH="$(printf '%s\n' "${REPORT}" | awk -v n="${UEFI_N}" '$1=="El" && $3=="img" && $4=="path" && $6==n {print $7; exit}')"
if [ -z "${EFI_IMG_PATH}" ]; then
	echo "FATAL: UEFI El Torito image ${UEFI_N} has no path in the report; report follows:" >&2
	cat "${DIST_DIR}/el-torito-report.txt" >&2
	exit 1
fi

xorriso -indev "${ISO_NAME}" -osirrox on -extract "${EFI_IMG_PATH}" /tmp/axonwall-efi.img >/dev/null 2>&1
mdir -i /tmp/axonwall-efi.img ::/efi/boot > /tmp/axonwall-efi-listing.txt 2>/dev/null \
	|| mdir -i /tmp/axonwall-efi.img ::/EFI/BOOT > /tmp/axonwall-efi-listing.txt 2>/dev/null \
	|| mdir -i /tmp/axonwall-efi.img :: > /tmp/axonwall-efi-listing.txt 2>/dev/null \
	|| true
grep -qiE 'bootx64|grubx64|gcdx64' /tmp/axonwall-efi-listing.txt || {
	echo "FATAL: EFI boot image lacks shim/grub binaries (Secure Boot chain broken); listing:" >&2
	cat /tmp/axonwall-efi-listing.txt >&2
	exit 1
}
echo "OK: El Torito BIOS (isolinux) + UEFI ($(basename "${EFI_IMG_PATH}") with shim/grub) present"

SIZE="$(stat -c%s "${ISO_NAME}")"
MAX_BYTES=$((2 * 1024 * 1024 * 1024))
if [ "${SIZE}" -gt "${MAX_BYTES}" ]; then
	echo "FATAL: ISO is ${SIZE} bytes — exceeds the 2 GiB release limit" >&2
	exit 1
fi

echo "== ISO factory complete: ${DIST_DIR}/${ISO_NAME}"
echo "   size: ${SIZE} bytes (limit ${MAX_BYTES})"
ls -la "${DIST_DIR}"
