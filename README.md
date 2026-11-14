# TOS 应用模板

面向 TOS 7 的 Vue 3 + TypeScript + Go 应用起点，直接依赖已发布的 [TOS App Kit](https://github.com/LuYifei2011/tos-app-kit)。模板包含可运行的请求示例、语言与主题设置、连接诊断、TOS 会话验证、Unix socket 服务以及 amd64 / arm64 Deb 打包。

## 创建应用

需要 Node.js 24、Python 3.10+ 和 `.go-version` 指定的 Go。在模板目录执行：

```sh
python3 scripts/create-app.py ../my-app \
  --id my-app --name 我的应用 --english-name 'My App' \
  --publisher 'Your Name' --email owner@your-domain.com \
  --help-url https://github.com/your-account/my-app
cd ../my-app
npm ci
go mod download
npm run build
```

命令创建独立项目并同步 npm 锁文件身份，不复制 Git 历史、node_modules 和构建产物。目标目录必须不存在。App Kit 的两个浏览器包通过固定版本 `v0.3.1` 的 GitHub Release URL 安装，Go module 使用 `v0.3.1`。依赖由 npm 锁文件和 go.sum 固定，项目无需检出库仓库。

也可复制整个模板，编辑 `app.json` 后执行 `python3 scripts/sync-metadata.py`。应用 ID 用于包名、TOS 账号、socket、API 前缀和偏好存储；安装后应保持稳定。

下游应用可选择开启[模板同步](docs/TEMPLATE_SYNC.md)，通过手动运行的 GitHub Actions 接收公共打包和服务工具更新 PR。默认关闭，首次运行默认预览；应用身份、业务代码和依赖版本由下游维护。

## 本地运行

```sh
npm run dev:server
```

打开 http://127.0.0.1:18947 查看生产构建。开发界面时保留该服务，在另一个终端执行 `npm run dev`，打开 http://127.0.0.1:5173 。Vite 转发应用 API，主题与语言默认回退浏览器。

示例 `GET /api/info` 显示连接状态，`POST /api/echo` 校验并返回文字，不保存内容。开发模式仅监听 loopback，并检查 Host 和 Origin。生产单元通过 Unix socket 提供 HTTP，所有 API 先检查凭据、CSRF、来源，再向固定本机 TOS 接口确认会话。

## 验证和打包

```sh
npm test
go vet ./...
npm run build
python3 scripts/build-package.py amd64
python3 scripts/build-package.py arm64
python3 scripts/verify-package.py build/*.deb
```

`npm test` 依次运行 `test:template`（生成器、元数据和安装脚本）与 `test:server`（应用路由及平台接入，包含 Go 竞态检测）。两个命令也可单独执行。CI 对模板和独立生成的应用分别使用这一入口。共享库的通用行为由 App Kit 自身测试覆盖。

安装包位于 `build/<id>_<version>_<arch>.deb`。模板附带占位图标和示例信息，可用于开发测试；发布检查要求填写真实信息并替换图标。新增业务时从 `src/App.vue` 和 `server/app.go` 开始，保留公共上下文及认证边界。标题栏和内容区使用 `TosAppShell`，业务操作图标使用 `@lucide/vue` 的静态导入。

详细说明见 [开发与发布](docs/DEVELOPMENT.md) 和 [界面规范](docs/DESIGN.md)。CI 检查模板生成、认证、双架构安装包、漏洞和官方包格式；Linux 真实 systemd 生命周期由专用测试验证。界面由人工审核，检查标题栏、窄屏布局、滚动、主题语言、键盘操作、请求示例和连接诊断。NAS 真机验收在目标设备完成。
