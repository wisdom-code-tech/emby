#!/bin/bash
set -euo pipefail

readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly PACKAGE_DIR="${PROJECT_ROOT}/packaging/emby"

required_files=(
  manifest config/privilege config/resource ICON.PNG ICON_256.PNG LICENSE
  app/ui/config app/ui/images/icon_64.png app/ui/images/icon_256.png
  app/server/UPSTREAM app/server/gateway-proxy
  app/server/emby-server/system/EmbyServer app/server/emby-server/bin/ffmpeg
  app/server/emby-server/licenses/license.docx
  cmd/main cmd/install_init cmd/install_callback cmd/uninstall_init cmd/uninstall_callback
  cmd/upgrade_init cmd/upgrade_callback cmd/config_init cmd/config_callback
  wizard/install wizard/config wizard/uninstall wizard/upgrade
)

for relative_path in "${required_files[@]}"; do
  [ -e "${PACKAGE_DIR}/${relative_path}" ] || { echo "缺少必需文件: ${relative_path}" >&2; exit 1; }
done

for json_file in config/privilege config/resource app/ui/config wizard/install wizard/config wizard/uninstall wizard/upgrade; do
  jq -e . "${PACKAGE_DIR}/${json_file}" >/dev/null
done

for script_file in "${PACKAGE_DIR}"/cmd/*; do
  bash -n "${script_file}"
  [ -x "${script_file}" ] || { echo "生命周期脚本不可执行: ${script_file}" >&2; exit 1; }
done

file "${PACKAGE_DIR}/app/server/emby-server/system/EmbyServer" | grep -q 'ELF 64-bit.*x86-64'
file "${PACKAGE_DIR}/app/server/gateway-proxy" | grep -q 'ELF 64-bit.*x86-64.*statically linked'
grep -q '^platform *= *x86' "${PACKAGE_DIR}/manifest"
upstream_version="$(sed -n 's/^version=//p' "${PACKAGE_DIR}/app/server/UPSTREAM")"
package_version="$(sed -n 's/^version[[:space:]]*=[[:space:]]*//p' "${PACKAGE_DIR}/manifest")"
case "${package_version}" in
  "${upstream_version}"|"${upstream_version}"-rc[0-9]*) ;;
  *) echo "manifest 与 Emby 上游版本不一致。" >&2; exit 1 ;;
esac
grep -Eq '^sha256=[0-9a-f]{64}$' "${PACKAGE_DIR}/app/server/UPSTREAM"
if find "${PACKAGE_DIR}" -iname '*docker*' -print -quit | grep -q .; then
  echo "原生包中不允许出现 Docker 相关文件。" >&2
  exit 1
fi

echo "FPK 静态校验通过。"
