<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { TosApiError } from 'tos-app-core'
import { useTosApp } from 'tos-app-core/vue'
import { TosAppShell, TosConnectionReport, TosPreferencesForm, TosPathInput } from 'tos-app-ui-kit'
import { Link, FolderSearch, Plus, Search, Pencil, Trash2, Type, X, Settings, ArrowRight, Square, RefreshCw, Folder, Info, ChevronDown, ArrowUp, ArrowDown, CheckCircle2, AlertCircle } from '@lucide/vue'
import { metadata } from './app'
import { deleteLinks } from './link-actions'
import appIcon from '../packaging/icon.svg'

type LinkItem = { path: string; name: string; target: string; resolved: string; status: string; kind: string; modified: string; fingerprint: string }
type Scan = { links: LinkItem[]; visited: number; directories: number; warnings: { path: string; code: string }[]; skipped: number; partial: boolean; reason: string }
type Action = 'create' | 'update' | 'rename' | 'delete'
const { core, language, accessFailure } = useTosApp()
const text = (zh: string, en: string) => language.value === 'en-US' ? en : zh
const root = ref(''), recursive = ref(true), query = ref(''), filter = ref('all'), page = ref(1)
const result = ref<Scan | null>(null), scannedPath = ref(''), busy = ref(false), saving = ref(false), connecting = ref(false), connected = ref(false)
const error = ref<TosApiError | null>(null)
const preferences = ref<HTMLDialogElement>(), preferencesOpen = ref(false)
function openPreferences() { preferences.value?.showModal(); preferencesOpen.value = true }
const selectedPaths = ref(new Set<string>()), showDetails = ref(false)
const modalOpen = ref(false), deletionItems = ref<LinkItem[]>([]), deleteProgress = ref(0)
const menu = ref<{ x: number; y: number } | null>(null), menuElement = ref<HTMLElement>()
let menuTrigger: HTMLElement | null = null
const toasts = ref<{ id: number; message: string; kind: 'success' | 'error' | 'info' }[]>([])
let toastID = 0
const toastTimers = new Map<number, ReturnType<typeof setTimeout>>()
function dismissToast(id: number) { clearTimeout(toastTimers.get(id)); toastTimers.delete(id); toasts.value = toasts.value.filter(t => t.id !== id) }
function toast(message: string, kind: 'success' | 'error' | 'info' = 'success') {
 const id = ++toastID
 toasts.value.push({ id, message, kind })
 if (kind !== 'error') toastTimers.set(id, setTimeout(() => dismissToast(id), 6000))
}
function pauseToast(id: number) { clearTimeout(toastTimers.get(id)); toastTimers.delete(id) }
function resumeToast(id: number) { if (toasts.value.find(t => t.id === id)?.kind !== 'error') toastTimers.set(id, setTimeout(() => dismissToast(id), 6000)) }
function clearSelection() { selectedPaths.value = new Set(); showDetails.value = false; closeMenu(false) }
function selectRow(item: LinkItem, event?: MouseEvent) {
 if (busy.value || saving.value) return
 const paths = new Set(event?.ctrlKey || event?.metaKey ? selectedPaths.value : [])
 if (paths.has(item.path)) paths.delete(item.path); else paths.add(item.path)
 selectedPaths.value = paths; showDetails.value = false
}
function toggleRow(item: LinkItem) {
 const paths = new Set(selectedPaths.value)
 if (paths.has(item.path)) paths.delete(item.path); else paths.add(item.path)
 selectedPaths.value = paths; showDetails.value = false
}
function togglePage() { selectedPaths.value = new Set(allChecked.value ? [] : shown.value.map(item => item.path)); showDetails.value = false }
function closeMenu(restore = false) { if (menu.value && restore) menuTrigger?.focus(); menu.value = null }
async function openMenu(event: MouseEvent | KeyboardEvent, item: LinkItem) {
 if (busy.value || saving.value || accessFailure.value) return
 event.preventDefault()
 if (!selectedPaths.value.has(item.path)) selectedPaths.value = new Set([item.path])
 menuTrigger = (event.currentTarget as HTMLElement).querySelector<HTMLElement>('.name-button')
 const rect = menuTrigger?.getBoundingClientRect()
 menu.value = { x: event instanceof MouseEvent ? event.clientX : rect?.left ?? 16, y: event instanceof MouseEvent ? event.clientY : rect?.bottom ?? 16 }
 await nextTick()
 const bounds = menuElement.value!.getBoundingClientRect()
 menu.value = { x: Math.max(8, Math.min(menu.value!.x, window.innerWidth - bounds.width - 8)), y: Math.max(8, Math.min(menu.value!.y, window.innerHeight - bounds.height - 8)) }
 menuElement.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
}
function menuKey(event: KeyboardEvent) {
 const buttons = [...menuElement.value!.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')]
 const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
 if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
  event.preventDefault()
  buttons[event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus()
 } else if (event.key === 'Escape' || event.key === 'Tab') { event.preventDefault(); closeMenu(true) }
}
function outsideMenu(event: PointerEvent) { if (!menuElement.value?.contains(event.target as Node)) closeMenu(false) }
function hideMenu() { closeMenu(false) }
const sortBy = ref<'name' | 'target' | 'status'>('name'), descending = ref(false)
const modal = ref<HTMLDialogElement>(), action = ref<Action>('create'), selected = ref<LinkItem | null>(null)
const parent = ref(''), name = ref(''), target = ref(''), allowBroken = ref(false), targetKind = ref<'file' | 'directory'>('directory')
const controller = new AbortController(); let scanController: AbortController | undefined
onUnmounted(() => { controller.abort(); scanController?.abort(); toastTimers.forEach(clearTimeout); document.removeEventListener('pointerdown', outsideMenu); window.removeEventListener('resize', hideMenu); window.removeEventListener('blur', hideMenu) })
const errors: Record<string, [string, string]> = {
 PATH_INVALID: ['请输入有效的绝对路径。', 'Enter a valid absolute path.'],
 PATH_OUTSIDE_SHARES: ['此路径不在当前账号可访问的共享目录中。', 'This path is outside your accessible shares.'],
 PATH_EXISTS: ['此名称已存在，请使用其他名称。', 'This name already exists. Choose another name.'],
 PATH_NOT_FOUND: ['目录或链接已不存在，请重新扫描。', 'The directory or link no longer exists. Scan again.'],
 FILESYSTEM_DENIED: ['应用账号无法访问此目录。请在 TOS 中为 symlink-manager 授予相应的共享目录权限。', 'The application account cannot access this directory. Grant symlink-manager the required shared-folder permissions in TOS.'],
 TOS_ACCESS_DENIED: ['当前 TOS 账号没有所需的目录权限。', 'Your TOS account lacks the required directory permissions.'],
 TOS_WRITE_DENIED: ['此目录为只读。', 'This directory is read-only.'],
 WRITE_PROBE_CLEANUP: ['写入权限检查未完成。请检查目录内的 .symlink-manager-…-permission 空文件及应用账号的写入权限。', 'Write verification did not complete. Check the .symlink-manager-…-permission file and the application account’s write access.'],
 DIRECTORY_LINK_OR_INVALID: ['请选择真实目录；扫描目录及其上级不能是符号链接。', 'Choose a real directory. The scan directory and its ancestors cannot be symlinks.'],
 LINK_CHANGED: ['链接已被其他操作修改，请重新扫描后检查。', 'The link changed. Scan again to check its current state.'],
 NOT_A_LINK: ['此路径已不是符号链接，请重新扫描。', 'This path is no longer a symbolic link. Scan again.'],
 TARGET_BROKEN: ['目标不存在。如需创建失效链接，请勾选允许目标暂不存在。', 'The target does not exist. Enable “Allow a missing target” to continue.'],
 TARGET_LOOP: ['目标会形成循环链接，请选择其他路径。', 'The target creates a symlink loop. Choose another path.'],
 TARGET_OUTSIDE: ['目标不在可访问的共享目录内。', 'The target is outside your accessible shares.'],
 TARGET_INACCESSIBLE: ['无法检查目标，请确认目标路径和访问权限。', 'Cannot inspect the target. Check its path and permissions.'],
 TARGET_UNCHECKED: ['目标检查未完成，请重试。', 'Target inspection did not complete. Retry.'],
 TARGET_INVALID: ['请输入有效的目标路径。', 'Enter a valid target path.'],
 NAME_INVALID: ['请输入文件名，不要包含斜杠或保留名称。', 'Enter a filename without slashes or reserved names.'],
 REQUEST_TIMEOUT: ['请求超时，结果可能尚未确认。请重新扫描并检查当前状态。', 'The request timed out and its outcome may be unconfirmed. Scan again to check the current state.'],
 ENTRY_LIMIT: ['目录条目过多，请手动输入更深一级的路径。', 'Too many directory entries. Enter a more specific path.'],
}
const errorText = computed(() => {
 const e = error.value ?? accessFailure.value; if (!e) return ''
 if (e.kind === 'session') return text('登录已失效，请从 TOS 重新打开应用。', 'Your session expired. Reopen the app from TOS.')
 if (e.kind === 'origin') return text('连接来源不匹配，请从 TOS 桌面重新打开应用。', 'The request origin does not match. Reopen the app from TOS.')
 if (e.kind === 'denied') return text('TOS 拒绝访问，请检查账号权限。', 'TOS denied access. Check your account permissions.')
 if (e.code === 'RECOVERY_REQUIRED') return text('目录发生并发变化，原条目已保留，请先检查恢复路径：', 'The directory changed concurrently. The original entry is preserved at: ') + (e.serverMessage ?? '')
 if (e.code && errors[e.code]) return text(...errors[e.code]!)
 return text('操作未完成，请检查连接并重新扫描确认当前状态。', 'The operation did not complete. Check the connection and scan again to confirm the current state.') + (e.code ? ` (${e.code})` : '')
})
function remember(e: unknown, prefix = '') { if (!controller.signal.aborted) { error.value = e instanceof TosApiError ? e : new TosApiError('invalid-response'); toast(prefix + errorText.value, 'error') } }
const statusLabel = (s: string) => ({ valid: text('正常', 'Valid'), broken: text('失效', 'Broken'), loop: text('循环', 'Loop'), inaccessible: text('无法访问', 'Inaccessible'), outside: text('范围外', 'Outside shares'), unchecked: text('未检查', 'Unchecked') }[s] ?? s)
const filtered = computed(() => (result.value?.links ?? []).filter(item => (filter.value === 'all' || (filter.value === 'attention' ? item.status !== 'valid' : item.status === filter.value)) && `${item.path}\n${item.target}`.toLocaleLowerCase().includes(query.value.toLocaleLowerCase())))
const sorted = computed(() => [...filtered.value].sort((a, b) => {
 const left = sortBy.value === 'status' ? statusLabel(a.status) : a[sortBy.value]
 const right = sortBy.value === 'status' ? statusLabel(b.status) : b[sortBy.value]
 return left.localeCompare(right, language.value, { numeric: true }) * (descending.value ? -1 : 1)
}))
const selectedItems = computed(() => shown.value.filter(item => selectedPaths.value.has(item.path)))
const focused = computed(() => selectedItems.value.length === 1 ? selectedItems.value[0]! : null)
const allChecked = computed(() => shown.value.length > 0 && selectedItems.value.length === shown.value.length)
const scopeChanged = computed(() => !!result.value && root.value !== scannedPath.value)
const relativeLocation = (item: LinkItem) => {
 const directory = item.path.slice(0, item.path.lastIndexOf('/'))
 return directory === scannedPath.value ? text('当前扫描目录', 'Scan directory') : directory.startsWith(scannedPath.value + '/') ? directory.slice(scannedPath.value.length + 1) : directory
}
function sort(column: 'name' | 'target' | 'status') {
 if (sortBy.value === column) descending.value = !descending.value
 else { sortBy.value = column; descending.value = false }
 page.value = 1
 clearSelection()
}
function clearFilters() { query.value = ''; filter.value = 'all'; page.value = 1 }
watch([query, filter, page], () => { clearSelection() })
const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / 100)))
const shown = computed(() => sorted.value.slice((Math.min(page.value, pages.value) - 1) * 100, Math.min(page.value, pages.value) * 100))
const linkPath = computed(() => `${parent.value.replace(/\/$/, '')}/${name.value}`)
const modalTitle = computed(() => ({ create: text('创建符号链接', 'Create symbolic link'), update: text('修改链接目标', 'Change link target'), rename: text('重命名链接', 'Rename link'), delete: text(`删除 ${deletionItems.value.length} 个符号链接`, `Delete ${deletionItems.value.length} symbolic links`) }[action.value]))
async function connect() {
 connecting.value = true; error.value = null; core.api.reset()
 try {
  await core.api.request('/info', { signal: controller.signal })
  const roots = await core.api.request<{ entries: { path: string }[] }>('/files/list', { signal: controller.signal })
  if (!root.value) root.value = roots.entries[0]?.path ?? ''
  connected.value = true
 } catch (e) { connected.value = false; remember(e) } finally { connecting.value = false }
}
async function scan(path = root.value, announce = true) {
 if (!path || busy.value) return
 busy.value = true; error.value = null; scanController = new AbortController()
 try {
  const response = await core.api.request<Scan>('/scan', { method: 'POST', body: { path, recursive: recursive.value }, signal: scanController.signal, timeoutMs: 30000 })
  result.value = response; scannedPath.value = path; root.value = path; page.value = 1; clearSelection()
  if (announce) toast(text(`扫描完成，找到 ${response.links.length} 个符号链接。`, `Scan complete. Found ${response.links.length} symbolic links.`), response.partial ? 'info' : 'success')
 } catch (e) { if (!scanController.signal.aborted) remember(e) }
 finally { busy.value = false; scanController = undefined }
}
function cancelScan() { scanController?.abort(); toast(text('扫描已取消，保留上次结果。', 'Scan cancelled. Previous results are retained.'), 'info') }
async function open(actionValue: Action, item: LinkItem | null = null) {
 if (busy.value || saving.value || accessFailure.value) return
 closeMenu(false)
 deletionItems.value = actionValue === 'delete' ? (item ? [item] : [...selectedItems.value]) : []
 if (actionValue === 'delete' && !deletionItems.value.length) return
 deleteProgress.value = 0
 action.value = actionValue; selected.value = item; error.value = null; allowBroken.value = false
 parent.value = item ? item.path.slice(0, item.path.lastIndexOf('/')) : root.value
 name.value = item?.name ?? ''; target.value = item?.target ?? ''; targetKind.value = item?.kind === 'file' ? 'file' : 'directory'
 await nextTick(); modal.value?.showModal(); modalOpen.value = true
 if (actionValue !== 'delete') modal.value?.querySelector<HTMLInputElement>(actionValue === 'update' ? '#target' : '#name')?.focus()
}
function close() { if (!saving.value) { modal.value?.close(); modalOpen.value = false; error.value = null } }
async function save() {
 if (saving.value) return
 saving.value = true; error.value = null
 try {
  if (action.value === 'delete') {
   const outcome = await deleteLinks(deletionItems.value, item => core.api.request('/links/delete', { method: 'POST', body: item, signal: controller.signal }), count => { deleteProgress.value = count })
   modal.value?.close(); modalOpen.value = false; clearSelection()
   await scan(scannedPath.value, false)
   if (outcome.failed) remember(outcome.error, text(`已删除 ${outcome.completed} 个链接；在「${outcome.failed.path}」处停止。其余链接未继续处理。`, `Deleted ${outcome.completed} links; stopped at “${outcome.failed.path}”. Remaining links were not processed. `))
   else toast(text(`已删除 ${outcome.completed} 个符号链接，目标文件已保留。`, `Deleted ${outcome.completed} symbolic links. Target files were preserved.`))
  } else {
   await core.api.request(`/links/${action.value}`, { method: 'POST', body: { path: action.value === 'create' ? linkPath.value : selected.value!.path, target: target.value, name: name.value, fingerprint: selected.value?.fingerprint ?? '', allowBroken: allowBroken.value }, signal: controller.signal })
   modal.value?.close(); modalOpen.value = false; clearSelection()
   toast(action.value === 'create' ? text('链接已创建。', 'Link created.') : action.value === 'rename' ? text('链接已重命名。', 'Link renamed.') : text('链接目标已更新。', 'Link target updated.'))
   const refreshPath = action.value === 'create' ? parent.value : scannedPath.value || parent.value
   if (action.value === 'create') { filter.value = 'all'; query.value = '' }
   await scan(refreshPath, false)
  }
 } catch (e) { remember(e) } finally { saving.value = false }
}
onMounted(() => { connect(); document.addEventListener('pointerdown', outsideMenu); window.addEventListener('resize', hideMenu); window.addEventListener('blur', hideMenu) })
</script>

<template>
 <TosAppShell :title="metadata.name[language]" scroll="hidden" content-class="manager-shell">
  <template #icon><img :src="appIcon" width="20" height="20" alt="" /></template>
  <div class="manager">
   <form class="location-bar" @submit.prevent="scan()">
    <Folder :size="17" class="location-icon" aria-hidden="true" />
    <label class="sr-only" for="scan-path">{{ text('扫描目录', 'Scan directory') }}</label>
    <TosPathInput id="scan-path" v-model="root" kind="directory" class="location-input" :disabled="busy || saving" placeholder="/Volume1/Public" required />
    <label class="check recursive"><input v-model="recursive" type="checkbox" :disabled="busy || saving" />{{ text('包含子目录', 'Subfolders') }}</label>
    <button v-if="busy" type="button" class="button secondary" @click="cancelScan"><Square :size="14" aria-hidden="true" />{{ text('停止', 'Stop') }}</button>
    <button v-else type="submit" class="button secondary scan-button" :disabled="!connected || !root || saving || !!accessFailure"><FolderSearch :size="16" aria-hidden="true" />{{ text('扫描', 'Scan') }}</button>
   </form>

   <div class="command-bar">
    <div class="commands" :aria-label="text('链接操作', 'Link actions')">
     <button class="button primary" :disabled="!connected || busy || saving || !!accessFailure" @click="open('create')"><Plus :size="16" aria-hidden="true" />{{ text('新建链接', 'New link') }}</button>
     <span class="toolbar-divider" aria-hidden="true"></span>
     <button class="button quiet edit-command" :disabled="!focused || busy || saving || !!accessFailure" :title="text('修改目标', 'Change target')" :aria-label="text('修改目标', 'Change target')" @click="focused && open('update', focused)"><Pencil :size="15" aria-hidden="true" /><span>{{ text('修改目标', 'Edit target') }}</span></button>
     <button class="button quiet rename-command" :disabled="!focused || busy || saving || !!accessFailure" :title="text('重命名', 'Rename')" :aria-label="text('重命名', 'Rename')" @click="focused && open('rename', focused)"><Type :size="16" aria-hidden="true" /><span>{{ text('重命名', 'Rename') }}</span></button>
     <button class="button quiet delete-command" :disabled="!selectedItems.length || busy || saving || !!accessFailure" :aria-label="text('删除所选链接', 'Delete selected links')" @click="open('delete')"><Trash2 :size="16" aria-hidden="true" /><span>{{ selectedItems.length > 1 ? text(`删除 (${selectedItems.length})`, `Delete (${selectedItems.length})`) : text('删除', 'Delete') }}</span></button>
    </div>
    <div class="list-controls">
     <label class="status-filter"><span class="sr-only">{{ text('链接状态', 'Link status') }}</span><select v-model="filter"><option value="all">{{ text('全部状态', 'All statuses') }}</option><option value="valid">{{ text('正常', 'Valid') }}</option><option value="attention">{{ text('需检查', 'Needs attention') }}</option><option value="broken">{{ text('失效', 'Broken') }}</option><option value="loop">{{ text('循环', 'Loop') }}</option></select></label>
     <label class="search"><Search :size="15" aria-hidden="true" /><input v-model="query" type="search" :aria-label="text('搜索链接或目标', 'Search links or targets')" :placeholder="text('搜索链接或目标', 'Search links or targets')" /></label>
     <button class="icon-button" :title="text('设置', 'Settings')" :aria-label="text('设置', 'Settings')" @click="openPreferences"><Settings :size="17" aria-hidden="true" /></button>
    </div>
   </div>

   <div v-if="accessFailure" class="message error" role="alert"><span>{{ errorText }}</span><button v-if="!connected || accessFailure" class="text-button" :disabled="connecting" @click="connect">{{ text('重新连接', 'Reconnect') }}</button></div>
   <details v-if="result?.partial" class="scan-warning"><summary>{{ text(`扫描未完成全部目录 · 跳过 ${result.skipped} 项`, `Partial scan · ${result.skipped} skipped`) }}</summary><p>{{ text('可能已达到扫描限制，请缩小目录范围继续扫描。', 'A scan limit may have been reached. Scan a smaller directory to continue.') }}</p><ul><li v-for="item in result.warnings" :key="item.path"><code>{{ item.path }}</code> — {{ errors[item.code] ? text(...errors[item.code]!) : item.code }}</li></ul></details>

   <section class="list-region" :aria-label="text('符号链接列表', 'Symbolic links')" :aria-busy="busy">
    <div class="table-scroll" @scroll="hideMenu">
     <table class="links-table">
      <colgroup><col class="selection-column" /><col class="name-column" /><col class="target-column" /><col class="status-column" /><col class="kind-column" /></colgroup>
      <thead><tr>
       <th scope="col" class="selection-cell"><input type="checkbox" :checked="allChecked" :indeterminate="selectedItems.length > 0 && !allChecked" :disabled="!shown.length || busy || saving" :aria-label="text('全选本页', 'Select this page')" @change="togglePage" /></th>
       <th scope="col" :aria-sort="sortBy === 'name' ? descending ? 'descending' : 'ascending' : 'none'"><button @click="sort('name')">{{ text('链接名称', 'Link name') }}<component :is="descending ? ArrowDown : ArrowUp" v-if="sortBy === 'name'" :size="12" aria-hidden="true" /></button></th>
       <th scope="col" :aria-sort="sortBy === 'target' ? descending ? 'descending' : 'ascending' : 'none'"><button @click="sort('target')">{{ text('目标路径', 'Target path') }}<component :is="descending ? ArrowDown : ArrowUp" v-if="sortBy === 'target'" :size="12" aria-hidden="true" /></button></th>
       <th scope="col" :aria-sort="sortBy === 'status' ? descending ? 'descending' : 'ascending' : 'none'"><button @click="sort('status')">{{ text('状态', 'Status') }}<component :is="descending ? ArrowDown : ArrowUp" v-if="sortBy === 'status'" :size="12" aria-hidden="true" /></button></th>
       <th scope="col">{{ text('目标类型', 'Target type') }}</th>
      </tr></thead>
      <tbody :class="{ refreshing: busy && result }">
       <tr v-for="item in shown" :key="item.path" :class="{ selected: selectedPaths.has(item.path) }" @click="selectRow(item, $event)" @contextmenu="openMenu($event, item)" @keydown.shift.f10="openMenu($event, item)" @keydown="($event.key === 'ContextMenu') && openMenu($event, item)" @dblclick="!busy && !saving && !accessFailure && open('update', item)">
        <td class="selection-cell" @click.stop @dblclick.stop><input type="checkbox" :checked="selectedPaths.has(item.path)" :disabled="busy || saving" :aria-label="text(`选择 ${item.name}`, `Select ${item.name}`)" @change="toggleRow(item)" /></td>
        <td><div class="name-cell"><Link :size="17" :stroke-width="1.6" aria-hidden="true" /><div class="name-text"><button class="name-button" :title="item.path" :aria-pressed="selectedPaths.has(item.path)" @click.stop="selectRow(item, $event)">{{ item.name }}</button><span class="secondary-path" :title="item.path">{{ relativeLocation(item) }}</span></div></div></td>
        <td><span class="target-path" :title="item.target">{{ item.target }}</span></td>
        <td><span class="link-status" :class="item.status"><span class="status-dot" aria-hidden="true"></span>{{ statusLabel(item.status) }}</span></td>
        <td class="kind-cell">{{ item.kind === 'directory' ? text('文件夹', 'Folder') : item.kind === 'file' ? text('文件', 'File') : '—' }}</td>
       </tr>
       <template v-if="busy && !result"><tr v-for="n in 6" :key="n" class="loading-row" aria-hidden="true"><td></td><td><span class="skeleton"></span></td><td><span class="skeleton"></span></td><td><span class="skeleton short"></span></td><td><span class="skeleton short"></span></td></tr></template>
      </tbody>
     </table>
     <div v-if="!shown.length && !busy" class="empty-state">
      <Link :size="30" :stroke-width="1.3" aria-hidden="true" />
      <h2>{{ !connected ? text('正在等待连接', 'Waiting for connection') : !result ? text('扫描目录中的符号链接', 'Scan for symbolic links') : query || filter !== 'all' ? text('没有匹配的链接', 'No matching links') : text('此目录中没有符号链接', 'No symbolic links in this directory') }}</h2>
      <p>{{ !connected ? text('连接成功后即可扫描和管理链接。', 'Connect to scan and manage links.') : !result ? text('在上方选择目录，扫描结果会显示在这里。', 'Choose a directory above. Scan results will appear here.') : query || filter !== 'all' ? text('试试其他关键词，或清除筛选条件。', 'Try another search or clear the filters.') : text('可以新建链接，或选择其他目录扫描。', 'Create a link or scan another directory.') }}</p>
      <button v-if="!connected && !connecting" class="button secondary" @click="connect">{{ text('重新连接', 'Reconnect') }}</button>
      <button v-else-if="connected && !result" class="button secondary" :disabled="!root || !!accessFailure" @click="scan()"><FolderSearch :size="15" />{{ text('开始扫描', 'Start scan') }}</button>
      <button v-else-if="result && (query || filter !== 'all')" class="text-button" @click="clearFilters">{{ text('清除筛选', 'Clear filters') }}</button>
      <button v-else-if="result" class="button secondary" :disabled="!!accessFailure" @click="open('create')"><Plus :size="15" />{{ text('新建链接', 'New link') }}</button>
     </div>
    </div>
   </section>

   <section v-if="focused && showDetails" class="link-details" :aria-label="text('链接详细信息', 'Link details')">
    <dl><dt>{{ text('链接位置', 'Link location') }}</dt><dd>{{ focused.path }}</dd><dt>{{ text('目标路径', 'Target path') }}</dt><dd>{{ focused.target }}</dd><dt>{{ text('修改时间', 'Modified') }}</dt><dd>{{ new Date(focused.modified).toLocaleString(language) }}</dd></dl>
    <button class="icon-button" :aria-label="text('关闭详细信息', 'Close details')" @click="showDetails = false"><X :size="16" /></button>
   </section>
   <footer class="status-bar">
    <p role="status" :title="scannedPath">{{ busy ? text('正在扫描…', 'Scanning…') : connecting ? text('正在连接…', 'Connecting…') : scopeChanged ? text('目录已更改，仍显示上次扫描结果', 'Directory changed; showing previous results') : result ? text(`${filtered.length} 个链接`, `${filtered.length} links`) : text('就绪', 'Ready') }}<span v-if="result && !busy && !scopeChanged" class="status-secondary">{{ text(`已检查 ${result.visited} 个条目`, `${result.visited} entries checked`) }}</span></p>
    <div class="status-actions"><span v-if="selectedItems.length" class="selection-count">{{ text(`已选择 ${selectedItems.length} 项`, `${selectedItems.length} selected`) }}</span><button v-if="focused" class="details-button" :aria-expanded="showDetails" @click="showDetails = !showDetails"><Info :size="14" aria-hidden="true" />{{ text('详细信息', 'Details') }}</button><div v-if="pages > 1" class="pagination"><button class="icon-button" :disabled="page <= 1" :aria-label="text('上一页', 'Previous page')" @click="page--"><ChevronDown class="previous-page" :size="16" /></button><span>{{ Math.min(page, pages) }} / {{ pages }}</span><button class="icon-button" :disabled="page >= pages" :aria-label="text('下一页', 'Next page')" @click="page++"><ChevronDown class="next-page" :size="16" /></button></div></div>
   </footer>
  </div>
  <dialog ref="preferences" class="preferences-dialog" aria-labelledby="preferences-title" @close="preferencesOpen = false">
   <div class="editor"><header class="dialog-heading"><h2 id="preferences-title">{{ text('设置', 'Settings') }}</h2><button class="icon-button" :aria-label="text('关闭', 'Close')" @click="preferences?.close()"><X :size="18" /></button></header><TosPreferencesForm /><div class="connection-settings"><span class="link-status" :class="connected ? 'valid' : 'inaccessible'"><span class="status-dot" aria-hidden="true"></span>{{ connected ? text('服务已连接', 'Connected') : text('尚未连接', 'Disconnected') }}</span><button class="button secondary" :disabled="connecting" @click="connect"><RefreshCw :size="15" />{{ text('重新连接', 'Reconnect') }}</button></div><TosConnectionReport /></div>
  </dialog>
  <dialog ref="modal" aria-labelledby="dialog-title" @cancel="saving ? $event.preventDefault() : close()">
   <form class="editor" @submit.prevent="save">
    <header class="dialog-heading"><h2 id="dialog-title">{{ modalTitle }}</h2><button type="button" class="icon-button" :disabled="saving" :aria-label="text('关闭', 'Close')" @click="close"><X :size="18"/></button></header>
    <template v-if="action === 'create'"><label class="field-label" for="parent">{{ text('链接保存目录', 'Link directory') }}<TosPathInput id="parent" v-model="parent" kind="directory" :disabled="saving" required/></label></template>
    <label v-if="action === 'create' || action === 'rename'" class="field-label" for="name">{{ text('链接名称', 'Link name') }}<input id="name" v-model="name" required maxlength="255" :disabled="saving" autocomplete="off" :autofocus="action === 'rename'"/></label>
    <div v-if="action === 'update' || action === 'rename'" class="current-link"><span class="muted">{{ text('链接位置', 'Link location') }}</span><code>{{ selected?.path }}</code></div>
    <template v-if="action === 'create' || action === 'update'">
     <div class="target-heading"><label class="field-label" for="target">{{ text('目标路径', 'Target path') }}</label><select v-model="targetKind" :disabled="saving" :aria-label="text('浏览目标类型', 'Browse target type')"><option value="directory">{{ text('浏览文件夹', 'Browse folders') }}</option><option value="file">{{ text('浏览文件', 'Browse files') }}</option></select></div>
     <TosPathInput id="target" v-model="target" :kind="targetKind" :disabled="saving" placeholder="/Volume1/Media 或 ../Media" required/>
     <p class="muted hint">{{ text('可输入绝对路径或相对路径。相对路径以链接所在目录为起点。', 'Enter an absolute or relative path. Relative paths start from the link’s directory.') }}</p>
     <label class="check"><input v-model="allowBroken" type="checkbox" :disabled="saving"/>{{ text('允许目标暂不存在', 'Allow a missing target') }}</label>
     <div class="link-preview"><code>{{ action === 'create' ? linkPath : selected?.path }}</code><ArrowRight :size="16" aria-hidden="true"/><code>{{ target || text('目标路径', 'Target path') }}</code></div>
    </template>
    <p v-if="action === 'rename'" class="muted">{{ text('仅修改链接名称，保留原目标路径。', 'Only the link name changes. Its stored target stays the same.') }}</p>
    <template v-if="action === 'delete'"><p>{{ text(`确定删除所选的 ${deletionItems.length} 个符号链接？目标文件和目录会保留。`, `Delete the ${deletionItems.length} selected symbolic links? Target files and folders will be preserved.`) }}</p><ul class="deletion-list"><li v-for="item in deletionItems" :key="item.path"><strong>{{ item.name }}</strong><code>{{ item.path }}</code><span>{{ text('目标：', 'Target: ') }}{{ item.target }}</span></li></ul><p v-if="saving" role="status">{{ text(`已删除 ${deleteProgress} / ${deletionItems.length}`, `Deleted ${deleteProgress} / ${deletionItems.length}`) }}</p></template>

    <footer class="dialog-footer"><button type="button" class="button secondary" :disabled="saving" @click="close">{{ text('取消', 'Cancel') }}</button><button type="submit" class="button" :class="action === 'delete' ? 'delete-button' : 'primary'" :disabled="saving">{{ saving ? text('正在处理…', 'Working…') : action === 'delete' ? text(`删除 ${deletionItems.length} 个链接`, `Delete ${deletionItems.length} links`) : text('保存', 'Save') }}</button></footer>
   </form>
  </dialog>
  <Teleport to="body">
   <div v-if="menu" ref="menuElement" class="context-menu" role="menu" :aria-label="text('链接操作', 'Link actions')" :style="{ left: `${menu.x}px`, top: `${menu.y}px` }" @keydown="menuKey">
    <button role="menuitem" :disabled="!focused" @click="focused && open('update', focused)"><Pencil :size="15" />{{ text('修改目标', 'Change target') }}</button>
    <button role="menuitem" :disabled="!focused" @click="focused && open('rename', focused)"><Type :size="16" />{{ text('重命名', 'Rename') }}</button>
    <div class="menu-divider"></div>
    <button role="menuitem" class="menu-delete" @click="open('delete')"><Trash2 :size="15" />{{ text(`删除 ${selectedItems.length} 个链接`, `Delete ${selectedItems.length} links`) }}</button>
   </div>
  </Teleport>
  <Teleport :to="modalOpen && modal ? modal : preferencesOpen && preferences ? preferences : 'body'">
   <div class="toast-stack" :aria-label="text('操作提示', 'Notifications')">
    <div v-for="item in toasts" :key="item.id" class="toast" :class="item.kind" :role="item.kind === 'error' ? 'alert' : 'status'" @mouseenter="pauseToast(item.id)" @mouseleave="resumeToast(item.id)" @focusin="pauseToast(item.id)" @focusout="resumeToast(item.id)">
     <component :is="item.kind === 'success' ? CheckCircle2 : item.kind === 'error' ? AlertCircle : Info" :size="18" aria-hidden="true" /><span>{{ item.message }}</span><button class="icon-button" :aria-label="text('关闭提示', 'Dismiss notification')" @click="dismissToast(item.id)"><X :size="15" /></button>
    </div>
   </div>
  </Teleport>
 </TosAppShell>
</template>
