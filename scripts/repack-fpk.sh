#!/bin/bash
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "用法: $0 <fpk-file> <app-directory>" >&2
  exit 1
fi

readonly FPK_FILE="$1"
readonly APP_DIR="$2"

[ -f "${FPK_FILE}" ] || { echo "FPK 文件不存在: ${FPK_FILE}" >&2; exit 1; }
[ -d "${APP_DIR}" ] || { echo "应用目录不存在: ${APP_DIR}" >&2; exit 1; }

work_dir="$(mktemp -d)"
trap 'rm -rf -- "${work_dir}"' EXIT
outer_dir="${work_dir}/outer"
verify_dir="${work_dir}/verify"
repacked_file="${work_dir}/repacked.fpk"
mkdir -p "${outer_dir}" "${verify_dir}"

tar -xzf "${FPK_FILE}" -C "${outer_dir}"

# fnpack 1.2.1 会把符号链接目标错误地写成链接自身。使用系统 tar
# 从原始 app 目录重建 app.tgz，同时排除 macOS 扩展属性。
tar --no-xattrs -czf "${outer_dir}/app.tgz" -C "${APP_DIR}" .

if command -v md5sum >/dev/null 2>&1; then
  app_checksum="$(md5sum "${outer_dir}/app.tgz" | awk '{print $1}')"
else
  app_checksum="$(md5 -q "${outer_dir}/app.tgz")"
fi

awk -v checksum="${app_checksum}" '
  BEGIN { replaced = 0 }
  /^checksum[[:space:]]*=/ {
    sub(/=.*/, "= " checksum)
    print
    replaced = 1
    next
  }
  { print }
  END {
    if (!replaced) print "checksum = " checksum
  }
' "${outer_dir}/manifest" > "${outer_dir}/manifest.new"
mv "${outer_dir}/manifest.new" "${outer_dir}/manifest"

tar --no-xattrs -czf "${repacked_file}" -C "${outer_dir}" \
  app.tgz LICENSE cmd config ICON.PNG ICON_256.PNG manifest wizard

tar -xzf "${outer_dir}/app.tgz" -C "${verify_dir}"
source_link_count="$(find "${APP_DIR}" -type l | wc -l | tr -d '[:space:]')"
packed_link_count="$(find "${verify_dir}" -type l | wc -l | tr -d '[:space:]')"
if [ "${packed_link_count}" != "${source_link_count}" ]; then
  echo "FPK 符号链接数量不一致：源目录 ${source_link_count}，包内 ${packed_link_count}。" >&2
  exit 1
fi
broken_link="$(find -L "${verify_dir}" -type l -print -quit 2>/dev/null || true)"
if [ -n "${broken_link}" ]; then
  echo "FPK 包含断裂或循环符号链接: ${broken_link}" >&2
  exit 1
fi
if tar -tzf "${repacked_file}" | grep -Eq '(^|/)(\.DS_Store|Thumbs\.db|\._[^/]*)$'; then
  echo "FPK 包含操作系统元数据文件。" >&2
  exit 1
fi

mv "${repacked_file}" "${FPK_FILE}"
echo "FPK 已重建并验证 ${packed_link_count} 个符号链接。"
