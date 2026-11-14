# 可选模板同步

生成的应用包含手动运行的 `Template sync` 工作流。默认关闭；开启后从
[tos-app-template](https://github.com/LuYifei2011/tos-app-template) 的 `main`
获取公共工具更新，以 PR 交给应用维护者审核。

## 开启与运行

1. 将生成的应用提交到自己的 GitHub 仓库。
2. 在 **Settings → Secrets and variables → Actions → Variables** 创建仓库变量
   `TEMPLATE_SYNC_ENABLED`，值为 `true`。
3. 在 **Settings → Actions → General → Workflow permissions** 允许 GitHub Actions
   创建 Pull Request。
4. 打开 **Actions → Template sync → Run workflow**，选择默认分支。第一次保留
   `dry_run` 勾选，查看日志和运行摘要中的变更范围。
5. 确认范围后再次运行，取消 `dry_run`，工作流会推送同步分支并创建 PR。
6. 审核 PR，完成应用自己的测试、构建和打包检查后合并。

工作流只在仓库变量开启、选择默认分支且当前仓库是下游应用时运行。删除变量或改为
`false` 即可关闭。重复运行同一上游提交会复用已有同步结果，不自动合并 PR。

## 同步范围

`.templatesyncignore` 使用 Git pathspec，默认排除全部文件，再逐项保留：

- `scripts/` 中的元数据、生成、构建、安全和服务检查脚本。
- `tests/test_template.py`、`tests/test_template_sync.py`。
- `packaging/debian/` 安装脚本、`packaging/service.in`、`packaging/socket.in`。
- `.go-version`、`.govulncheck-version`、`.gitattributes`、`.gitignore`。

应用身份、图标、前后端源码、npm 与 Go 依赖清单及锁文件、应用文档和工作流均由下游维护。
App Kit 升级通过修改依赖版本完成；模板对 CI 和业务接入示例的改进可在上游提交中查看，
按应用需要手动移植。工具链更新也可能需要同步调整下游依赖。

忽略文件随生成器一起复制。该 Action 会还原同步前已跟踪的忽略文件，不能依赖同步 PR
自动更新这份规则；上游扩展同步范围时，下游维护者应先审核并手动更新规则。共享脚本中
如果已有应用定制，同样需要逐项审核同步 PR；合并策略会优先采用模板版本。

## Token 与 CI

公开模板可以使用内置 `GITHUB_TOKEN`。工作流只要求当前仓库的 Contents 和 Pull requests
写权限；默认同步范围排除了 `.github/workflows/`。

如果同步 PR 的 CI 等待批准，在 PR 中选择 **Approve workflows to run**。
如需让 CI 自动启动，可将仅授权当前应用仓库、具有 Contents 和 Pull requests 写权限的
token 存为 `TEMPLATE_SYNC_TOKEN`；工作流会用它推送分支和创建 PR。
具体触发规则见 [GitHub 文档](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow#triggering-a-workflow-from-a-workflow)。

Action 固定到 `v2.6.0` 的提交。升级 Action 或调整同步规则时，先运行预览并检查完整差异。
用法与参数见 [actions-template-sync](https://github.com/AndreasAugustin/actions-template-sync)。
