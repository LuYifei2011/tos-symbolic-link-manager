# 开发与发布

## 项目结构

| 路径 | 修改内容 |
| --- | --- |
| `app.json` | 应用 ID、版本、中英文名称与描述、发布者、邮箱、帮助链接、beta 状态 |
| `src/app.ts` | 每应用独立的 App Kit 上下文、API 选项和偏好读取 |
| `src/App.vue`、`src/style.css` | 业务界面和样式 |
| `server/app.go` | 业务路由、参数校验和静态资源 |
| `server/platform.go` | TOS 平台会话验证及错误映射 |
| `server/main.go` | 开发入口、systemd socket 和退出生命周期 |
| `packaging/` | 图标、服务单元和安装生命周期脚本 |
| `scripts/`、`tests/` | 初始化、元数据、打包和验证 |

`app.json` 编译进 Go 二进制；前端只打包 ID、版本和名称。打包脚本从同一文件生成 config.ini、语言文件、控制元数据、单元名和安装路径，不需要全仓搜索替换。修改身份或版本后执行 `python3 scripts/sync-metadata.py`，同步并提交两个 npm 文件。

应用框架通过 UI kit 的 `TosAppShell` 提供，只包含标题栏和内容 main。业务组件放在默认插槽中；可用 icon 插槽传入应用图标，contentClass 和 scroll 参数控制内容布局与滚动。标题栏右侧默认预留 180px，长标题截断，620px 以下整页滚动。通用操作图标静态导入 `@lucide/vue`，按需打包。

浏览器公共库使用 App Kit `0.3.1` 的固定版本 Release URL，由 npm 锁文件固定完整性。Go module 同样使用 v0.3.1，由 go.sum 校验。生成的项目无需检出公共库源码。

Go module 名 `tos-app-template` 用于项目内部导入，与 TOS 应用 ID 独立；下游不发布 Go 库时可保留。如果改为自己的模块路径，同时修改 `config.go` 的两个导入位置及测试导入。

## 认证与权限

生产入口默认启用 TOS 模式，没有自动降级为开发模式的路径。`--dev` 仅由本地脚本显式传入；生产 unit 没有该参数，也不监听 TCP。`RequireNonRoot` 检查业务账号，特权 `--prepare-data` 在 systemd 启动前仅初始化该应用的固定数据目录。

每次 API 请求先执行 `tosapp.Authenticate`，再用本机 `http://127.0.0.1:8181/v2/fileManage/homeList` 验证会话。此只读验证方式来自 batch-rename，具有应用对应平台接口的访问要求。若业务采用其他平台 API，应在 `platform.go` 替换为该接口支持的真实会话验证方式，并保留响应格式与登录状态检查。不能仅凭 Cookie 存在就视为已登录。

会话通过只说明平台当前接受凭据。文件、任务和其他资源的权限仍须由每个业务接口确认；不要直接用应用系统账号访问用户指定的共享路径。模板的 echo 不访问用户文件。响应和日志不输出 Cookie、CSRF 或平台原始正文。平台 URL 由代码固定，不能接收用户指定的转发目标。

前端在会话失效时跳转 `/tos/` 登录页，由平台处理登录后的返回流程。403 时显示权限或来源提示并隐藏应用内容；访问失败后停止业务请求。权限调整后从 TOS 桌面重新打开应用。

## 数据与安装

打包遵循 [TOS Deb 开发指南 §8.4 / §8.12 / §8.14](https://github.com/terramaster-tos/tos-app-pkg-tools/blob/main/TOS%207%20Application%20Development%20Guide/08_Deb_Development.md)：`low_version` 使用纯数字 `7.0`，生命周期脚本使用 `#!/bin/bash` 和 0755 权限，服务单元保存在应用的 `init.d` 内。打包时统一脚本、语言和服务配置的 LF 换行。开发脚本 `scripts/dev.sh` 在本机运行。

模板运行时只预留 `/usr/local/<id>/data`，示例请求不写文件。目录由应用账号持有，权限 0700。新增数据应存放在此处，并自行实现原子写入、大小上限与资源权限隔离。

安装将 `webui.bz2` 解压到临时目录，再替换 site，升级清理旧静态文件。TOS 可能在 deb 配置之后才创建应用账号，安装脚本会等待；账号就绪后通过应用中心启用应用，由特权 pre-start 初始化数据。生命周期脚本不创建或删除平台账号。remove 和 purge 都保留 data；静态页面、socket 和包内文件被移除。需要彻底删除数据时，由应用产品定义明确操作。

## 自动化与发布

下游可按[可选模板同步](TEMPLATE_SYNC.md)开启公共工具更新 PR。共享打包脚本与业务代码采用不同的更新范围，合并前需完成应用自己的验证。

`.github/workflows/release.yml` 对 main、PR 和手动运行执行测试、构建与打包。推送 `v<版本>` 标签时额外检查真实发布信息、占位图标和版本匹配，所有检查通过后发布 Deb 与 SHA256SUMS。模板自身的示例身份无法通过发布门槛。

本地发布前：

1. 修改 `app.json` 的真实信息和版本，替换 `packaging/icon.svg`，修改业务描述与 `docs/RELEASE_NOTES.md`。
2. 执行 `python3 scripts/sync-metadata.py` 和 `python3 scripts/check.py --release --tag v<版本>`。
3. 完成 README 中的验证，运行 `python3 scripts/check-security.py build/*.deb` 扫描锁定 npm 依赖、双架构 Go 源码和包内二进制。
4. 在 TOS 真机检查安装、桌面入口、登录过期、主题语言、启动停止、升级和卸载后的数据保留。
5. 提交并推送，通过 CI 后为同一提交创建签名标签并推送。新版本使用新标签，已有 Release 不覆盖。

Node 使用 24；Go 打包工具链由 `.go-version` 固定并禁止自动切换；漏洞扫描器版本见 `.govulncheck-version`。依赖更新必须同时提交锁文件，App Kit 的两个浏览器包应保持同一版本。

界面由人工审核。运行 `npm run dev:server` 后，按[界面规范](DESIGN.md)检查标题栏和 TOS 窗口控件留白、长标题、桌面与窄屏滚动、主题语言切换及刷新后的偏好保留、键盘焦点和请求示例。模拟服务断开后检查连接诊断及重新连接，并在 TOS 真机复核实际入口和会话状态。

Linux 的 `scripts/test-service.py` 会安装测试包、创建临时系统账号，再检查真实 systemd 服务；它只允许在隔离的 GitHub Actions Linux runner 中以 root 运行，不能在 NAS 执行。
