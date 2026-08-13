# Emby for fnOS

基于 Emby 官方 Linux x86_64 Debian 发行包构建的飞牛 fnOS 原生 FPK。应用运行期不依赖 Docker，使用 Go Unix Socket 代理接入 fnOS 统一网关。

## 当前版本

- Emby Server：`4.9.5.0`
- FPK：`4.9.5.0-rc1`
- 平台：fnOS x86_64，最低版本 `1.1.3100`
- 统一网关路径：`/app/emby`
- 代理上游：`127.0.0.1:8096`

## 构建

```bash
make build
```

构建流程会：

1. 下载官方 `emby-server-deb_4.9.5.0_amd64.deb`。
2. 校验固定的 SHA-256。
3. 提取完整 Emby 运行树和官方许可文件。
4. 静态编译 Linux x86_64 Go 网关代理。
5. 校验 FPK 结构、JSON、Shell、ELF 和上游元数据。
6. 使用 fnpack 1.2.1 生成 `dist/emby.fpk`。

## GitHub Actions

工作流每 6 小时检查一次 Emby 官方最新稳定版。首次发现新版本时，自动下载官方 amd64 Debian 资产、校验 GitHub Release 提供的 SHA-256、构建 FPK，并发布候选 Release。

- 定时任务：发布 `上游版本-rcN` 候选版。
- 手动运行并启用 `stable`：发布不带 `rc` 后缀的正式版。
- 同一上游版本更新打包实现时，递增根目录的 `PACKAGING_REVISION`。
- `force` 仅用于重建同一标签并覆盖资产。

## 数据与访问

- Emby 程序数据：`TRIM_PKGVAR/programdata`
- 缓存：`TRIM_PKGVAR/cache`
- 临时转码：`TRIM_PKGVAR/transcoding-temp`
- 日志：`TRIM_PKGVAR/emby.log`、`TRIM_PKGVAR/gateway.log`
- 媒体目录：安装后在 fnOS 应用设置中授权给 Emby，再到 Emby 管理后台添加媒体库。

`8096` 是 Go 代理使用的固定上游端口，不应在 Emby 网络设置中修改；修改后会导致代理无法连接。Emby 首次设置完成后，应在“服务器 → 网络 → 本地 IP 地址”中填写 `127.0.0.1`，确保该端口只监听回环地址。这个设置属于 Emby 自己的数据配置，FPK 不在首次启动前猜写其私有配置格式。

## 已知边界

- Emby 是闭源商业软件，本项目只重新打包官方二进制，不修改 Emby 本体；完整上游许可随官方运行树一并保留。
- 硬件转码依赖 fnOS 对应用用户开放 `/dev/dri` 及对应驱动。本地静态检查无法替代真机验证。
- fnOS 统一网关可能要求 fnOS 登录态；电视、手机等原生 Emby 客户端能否直接使用该入口，需要在真机验证网关鉴权策略。
- Emby 首次启动的默认监听范围需在真机确认；完成向导后应显式把本地 IP 地址设为 `127.0.0.1`。
