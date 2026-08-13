#!/bin/bash
set -euo pipefail

readonly EMBY_VERSION="${EMBY_VERSION:-4.9.5.0}"
readonly DEFAULT_VERSION="4.9.5.0"
readonly DEFAULT_SHA256="1d718ffa0169c393de3eafda65b1b057a3db4ead93ffeb5883abd01735de9843"
readonly ASSET_NAME="emby-server-deb_${EMBY_VERSION}_amd64.deb"
readonly ASSET_URL="https://github.com/MediaBrowser/Emby.Releases/releases/download/${EMBY_VERSION}/${ASSET_NAME}"
readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly PACKAGE_DIR="${PROJECT_ROOT}/packaging/emby"
readonly SERVER_DIR="${PACKAGE_DIR}/app/server"
readonly RUNTIME_DIR="${SERVER_DIR}/emby-server"
readonly CACHE_DIR="${PROJECT_ROOT}/.cache"
readonly DEB_FILE="${CACHE_DIR}/${ASSET_NAME}"

if [ -n "${EMBY_DEB_SHA256:-}" ]; then
  EXPECTED_SHA256="${EMBY_DEB_SHA256}"
elif [ "${EMBY_VERSION}" = "${DEFAULT_VERSION}" ]; then
  EXPECTED_SHA256="${DEFAULT_SHA256}"
else
  echo "非默认 Emby 版本必须通过 EMBY_DEB_SHA256 提供官方资产哈希。" >&2
  exit 1
fi
readonly EXPECTED_SHA256

for command_name in ar curl file shasum tar; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    echo "缺少构建依赖: ${command_name}" >&2
    exit 1
  }
done

install_icons() {
  icon_source="${RUNTIME_DIR}/system/dashboard-ui/images/icon-512x512.png"
  [ -f "${icon_source}" ] || { echo "Emby 运行树中缺少官方图标。" >&2; exit 1; }
  mkdir -p "${PACKAGE_DIR}/app/ui/images"
  if command -v sips >/dev/null 2>&1; then
    sips -z 256 256 "${icon_source}" --out "${PACKAGE_DIR}/ICON_256.PNG" >/dev/null
    sips -z 64 64 "${icon_source}" --out "${PACKAGE_DIR}/ICON.PNG" >/dev/null
  elif command -v magick >/dev/null 2>&1; then
    magick "${icon_source}" -resize 256x256! "${PACKAGE_DIR}/ICON_256.PNG"
    magick "${icon_source}" -resize 64x64! "${PACKAGE_DIR}/ICON.PNG"
  elif command -v convert >/dev/null 2>&1; then
    convert "${icon_source}" -resize 256x256! "${PACKAGE_DIR}/ICON_256.PNG"
    convert "${icon_source}" -resize 64x64! "${PACKAGE_DIR}/ICON.PNG"
  else
    echo "缺少 PNG 缩放工具（sips 或 ImageMagick）。" >&2
    exit 1
  fi
  cp "${PACKAGE_DIR}/ICON_256.PNG" "${PACKAGE_DIR}/app/ui/images/icon_256.png"
  cp "${PACKAGE_DIR}/ICON.PNG" "${PACKAGE_DIR}/app/ui/images/icon_64.png"
}

if [ -x "${RUNTIME_DIR}/system/EmbyServer" ] && \
   grep -Fxq "version=${EMBY_VERSION}" "${SERVER_DIR}/UPSTREAM" 2>/dev/null; then
  install_icons
  echo "Emby ${EMBY_VERSION} 上游产物已存在，跳过下载。"
  exit 0
fi

mkdir -p "${CACHE_DIR}"
if [ ! -f "${DEB_FILE}" ]; then
  curl -fL --retry 3 "${ASSET_URL}" -o "${DEB_FILE}.download"
  mv "${DEB_FILE}.download" "${DEB_FILE}"
fi
actual_sha256="$(shasum -a 256 "${DEB_FILE}" | awk '{print $1}')"
if [ "${actual_sha256}" != "${EXPECTED_SHA256}" ]; then
  echo "Emby 官方资产校验失败。" >&2
  echo "期望: ${EXPECTED_SHA256}" >&2
  echo "实际: ${actual_sha256}" >&2
  exit 1
fi

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT
(
  cd "${work_dir}"
  ar x "${DEB_FILE}"
)
mkdir -p "${work_dir}/root"
data_archive="$(find "${work_dir}" -maxdepth 1 -type f -name 'data.tar.*' -print -quit)"
[ -n "${data_archive}" ] || { echo "Debian 包中缺少 data.tar。" >&2; exit 1; }
tar -xf "${data_archive}" -C "${work_dir}/root"
source_runtime="${work_dir}/root/opt/emby-server"
[ -x "${source_runtime}/system/EmbyServer" ] || { echo "官方包中缺少 EmbyServer。" >&2; exit 1; }
[ -x "${source_runtime}/bin/ffmpeg" ] || { echo "官方包中缺少 FFmpeg。" >&2; exit 1; }

rm -rf "${RUNTIME_DIR}"
mkdir -p "${SERVER_DIR}"
cp -R "${source_runtime}" "${RUNTIME_DIR}"
chmod 0755 "${RUNTIME_DIR}/system/EmbyServer" "${RUNTIME_DIR}"/bin/*

printf '%s\n' \
  "version=${EMBY_VERSION}" \
  "source=https://github.com/MediaBrowser/Emby.Releases/releases/tag/${EMBY_VERSION}" \
  "asset=${ASSET_NAME}" \
  "sha256=${EXPECTED_SHA256}" \
  > "${SERVER_DIR}/UPSTREAM"

binary_type="$(file "${RUNTIME_DIR}/system/EmbyServer")"
case "${binary_type}" in
  *ELF*64-bit*x86-64*) ;;
  *) echo "EmbyServer 不是 Linux x86_64 ELF: ${binary_type}" >&2; exit 1 ;;
esac

install_icons
echo "已提取 Emby Server ${EMBY_VERSION}。"
