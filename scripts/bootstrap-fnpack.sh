#!/bin/bash
set -euo pipefail

readonly FNPACK_VERSION="1.2.1"
readonly DARWIN_ARM64_SHA256="36c798c434277dda8c52996e3f0a69a73b583458b2600d1d0374b9078805fb7a"
readonly DARWIN_AMD64_SHA256="20a633f11d2b1ee188ab0ad5690538679c06dc96647b0162b9c1939e834a61bd"
readonly LINUX_AMD64_SHA256="72d2a4095da676b64510b023731a227b369d80f8079bc45ff8a2f802ec0480c1"
readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly TOOL_DIR="${PROJECT_ROOT}/.tools"
readonly TOOL_PATH="${TOOL_DIR}/fnpack"

if command -v fnpack >/dev/null 2>&1 || [ -x "${TOOL_PATH}" ]; then
  exit 0
fi

case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) platform="darwin-arm64"; expected_sha256="${DARWIN_ARM64_SHA256}" ;;
  Darwin/x86_64) platform="darwin-amd64"; expected_sha256="${DARWIN_AMD64_SHA256}" ;;
  Linux/x86_64) platform="linux-amd64"; expected_sha256="${LINUX_AMD64_SHA256}" ;;
  *)
    echo "当前平台不支持自动引导 fnpack ${FNPACK_VERSION}。" >&2
    exit 1
    ;;
esac

mkdir -p "${TOOL_DIR}"
temp_path="${TOOL_PATH}.download"
curl -fsSL --retry 3 "https://static2.fnnas.com/fnpack/fnpack-${FNPACK_VERSION}-${platform}" -o "${temp_path}"
actual_sha256="$(shasum -a 256 "${temp_path}" | awk '{print $1}')"
if [ "${actual_sha256}" != "${expected_sha256}" ]; then
  echo "fnpack 校验失败，拒绝使用下载文件。" >&2
  exit 1
fi
chmod 0755 "${temp_path}"
mv "${temp_path}" "${TOOL_PATH}"
