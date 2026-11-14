# 开发与 NAS 验收

## 结构

- `server/filesystem.go`：有界扫描、目标解析、目录选择 API、链接操作。
- `server/rename_linux.go` / `rename_darwin.go`：不覆盖的系统重命名与文件时间。
- `server/platform.go`：TOS 登录、每请求共享目录和祖先校验、写入权限探测。
- `server/app.go`：来源校验、API 路由和静态资源。
- `src/App.vue`：扫描、搜索筛选、管理对话框、设置。
- `app.json`：身份与版本；`scripts/sync-metadata.py` 同步 npm 元数据。

生产 API 保留 TOS Cookie、CSRF 和来源校验。凭据只存在于请求内，不持久化。生产访问共享目录与系统文件访问权限取交集。服务账号的授权由管理员配置。创建权限探测不能替代用户细粒度删除 ACL；应将本管理工具的应用访问权限授予可信任的管理用户。

文件操作固定每一级真实目录描述符，使用 `O_NOFOLLOW`、`fstatat(AT_SYMLINK_NOFOLLOW)`、`readlinkat`、`symlinkat` 和 `unlinkat`。重命名使用 Linux `renameat2(RENAME_NOREPLACE)`，macOS 开发环境使用 `renameatx_np(RENAME_EXCL)`。NAS 内核或文件系统不支持不覆盖重命名时，操作失败，不降级为覆盖式重命名。

原链接先移入随机名称、0700 权限的同目录工作目录，重新核对 inode、mtime、模式和目标指纹，然后完成操作。失败时不覆盖恢复；若源路径被其他进程抢占，返回 `RECOVERY_REQUIRED` 和恢复路径。保留目录需管理员检查后恢复。该机制不保证崩溃后自动恢复或原子更新。

## API

所有业务请求先验证 TOS 会话（开发模式例外）。错误使用 `{ "code": "..." }`，文件权限错误返回 409，避免把单个目录的失败当成整个应用会话失效。

| API | 请求 / 返回 |
| --- | --- |
| `GET /api/info` | 应用 ID、版本、运行模式 |
| `GET /api/files/list?path=...` | 无 path 返回共享根；有 path 返回可浏览条目，排除符号链接 |
| `GET /api/files/stat?path=...` | 目录选择器元数据；仅叶子明确缺失返回 null |
| `POST /api/scan` | `{path, recursive}`；返回 links、visited、directories、warnings、skipped、partial、reason |
| `POST /api/links/create` | `{path, target, allowBroken}` |
| `POST /api/links/update` | `{path, target, fingerprint, allowBroken}` |
| `POST /api/links/rename` | `{path, name, fingerprint}`；只改同目录名称 |
| `POST /api/links/delete` | `{path, fingerprint}`；只删除链接 |

HTTP 请求上下文 25 秒；客户端超时 30 秒。扫描时间预算 15 秒，但正在进行的 TOS 请求仍受请求总超时约束。部分结果明确标记；传输失败时不当作完整结果。扫描不缓存用户目录授权到下一请求。

## 自动测试

`npm test` 运行前端批量删除顺序、失败停止与确认快照测试，以及元数据 / 安装 / 模板同步测试及 Go race 测试。覆盖 API 来源与 CSRF、平台登录响应、目录权限探测、目标路径解析、循环和失效链接、不跟随目录链接、并发创建、同名冲突、过时指纹、目标保留、取消和深度限制。`go vet ./...` 和 `npm run build` 分别检查 Go 和前端。

## NAS 验收清单

仅在专门测试共享目录准备文件，不要直接在生产数据上验收：

1. 安装对应架构的 Deb，从 TOS 桌面打开，确认会话、浅深主题、中英文和目录浏览。
2. 给应用账号配置测试目录的读取 / 遍历权限，扫描现有文件、目录、相对链接、绝对链接、失效链接与循环链接。
3. 给应用账号添加测试目录写入权限，创建链接、修改目标、重命名、删除；核对目标文件内容与哈希不变。
4. 用普通用户验证无权限目录、只读目录、创建权限与删除权限不同的 ACL；只有可信任管理用户应拥有应用访问权限。确认失效会话和跨来源请求拒绝执行。
5. 测试同名普通文件、同名目录、同名失效链接的冲突均不会覆盖。扫描后从其他终端更改链接，再执行管理动作应拒绝。
6. 测试正常与异常停止服务后的随机工作目录；确认错误给出恢复路径、原条目保留。
7. 测试应用停用、重新启用和升级；检查 socket 生命周期、平台页面与后端版本一致、旧静态资源清除及 data 内容不变。分别测试保留配置和删除配置卸载后的真实路径、数据、账号和组，再重装核对恢复。

NAS 已报告安装阶段的 `216/GROUP`：应用账号存在，但同名组不存在。已修正安装时的组初始化，待目标 NAS 复测。[v0.1.0 发布 CI](https://github.com/LuYifei2011/tos-symbolic-link-manager/actions/runs/43563084023) 已通过 Linux systemd、官方包检查和漏洞扫描；它不覆盖 TOS 应用中心卸载选项的实际行为。

批量删除复用 `POST /api/links/delete`，前端 `src/link-actions.ts` 对确认时的路径和指纹取快照，顺序提交。遇到任何错误即停止，保留已完成计数，刷新列表后通过 toast 告知未完成情况；不会自动重试不确定的删除请求。

## 静态更新与数据保留

postinst 在首次安装和升级时从同一份 `webui.bz2` 刷新后端 `site` 和平台 `webui`。两者均先检查真实目录、临时展开和验证首页，再切换并清理旧资源；目录切换失败时恢复该目录旧版本。这保证正常升级同时更新两个入口，不承诺两个目录的原子切换或断电恢复。

包 postrm 在 remove 和 purge 时清理上述静态目录、对应 next/previous 临时目录及 socket，保留 data、账号和组。应用中心可能额外删除实际安装目录。开发文档的独立夹具在 TOS 7.0.1422 / x86_64 上已观察到：保留配置卸载后卷上 data 保留，删除配置卸载后实际应用目录及 data 被删除，账号和组均保留。符号链接管理器自身仍需分别验证这两条路径；上述夹具结果仅覆盖所记录的固件和环境。

| 运行时路径 | 创建方、用途和权限 | 增长与卸载策略 |
| --- | --- | --- |
| `/usr/local/symlink-manager` | TOS 安装根映射；可能指向卷上的 `@apps/symlink-manager` | 平台维护；记录真实路径后检查卸载结果 |
| `data/`（相对安装根） | 特权 pre-start 创建，应用账号持有，0700；当前业务未在此持久化配置 | 包脚本保留；应用中心删除配置可能移除整个应用目录 |
| `site/`、`webui/`、各自 `.next` / `.previous` | postinst 从包内归档展开；0755 目录，静态页面只读访问 | 随构建资源增长；升级替换，包卸载脚本清理 |
| `/var/api/symlink-manager.sock` | systemd socket 创建，应用用户与专用组，0660 | 停用或卸载移除；无持久内容 |
| 共享目录内 `.symlink-manager-<随机值>-permission` | TOS 以当前用户创建的空文件；权限由平台和共享目录 ACL 决定 | 正常请求立即删除；中断残留需管理员核对后处理，卸载脚本不扫描共享目录 |
| 共享目录内 `.symlink-manager-<随机值>/original` | 应用账号创建 0700 工作目录并暂存原链接，用于安全修改与恢复 | 正常操作清理空目录；失败或中断可能保留原条目，按错误中的恢复路径人工检查；卸载保留 |
| 用户链接及链接目标 | 用户业务文件；应用仅管理授权范围内的链接，目标内容不改写 | 由用户管理，卸载脚本不删除 |

## 服务账号与 socket

服务配置使用 `User=symlink-manager`、`Group=symlink-manager`，socket 使用相同的用户和专用组，权限为 `0660`，与 app-pkg-tools §8.12 / §8.3.1 的组配置一致。

目标 NAS 的日志显示启用时无法解析同名组。`postinst configure` 在首次安装和升级时查询专用组，仅在组确实不存在时创建同名系统组，确认非 root GID 后再注册、启用服务。已有组及 GID 保持不变；查询、创建失败直接使安装失败并报告原因。

应用用户仍由 TOS 创建。如果 `postinst` 执行时用户尚不存在，安装完成后等待 TOS 创建用户并启用服务；已有用户时直接启动。安装脚本不更改用户主组或共享目录权限。包卸载脚本保留账号组，以免已有文件的 GID 失去对应关系；应用中心的额外清理由平台决定。

安装脚本测试覆盖用户尚未创建、已有用户、已有组、重复安装、升级、查询和创建失败，检查组先于 systemctl 操作准备好。Linux 生命周期测试模拟平台在安装后创建用户、使用不同名称的主组，检查服务与 socket 的专用 GID；v0.1.0 发布 CI 已运行通过。账号收尾由测试自身执行，不能作为安装包自动清理账号的证明。


参考实现：[tos-cadviewer 的 postinst](https://github.com/le3gold/tos-cadviewer/blob/main/DEBIAN/postinst) 在安装阶段补齐缺失组，并记录了 TOS 7.0.1201 上用户延迟创建及 `216/GROUP` 的情况。此处采用其安装阶段分工，失败处理由本项目独立实现。

参考：[官方服务规范 §8.12](https://github.com/terramaster-tos/tos-app-pkg-tools/blob/main/TOS%207%20Application%20Development%20Guide/08_Deb_Development.md#812-systemd-service-specification)、[官方 socket 规范 §8.3.1](https://github.com/terramaster-tos/tos-app-pkg-tools/blob/main/TOS%207%20Application%20Development%20Guide/08_Deb_Development.md#831-webui-internal-open-iframe)。
