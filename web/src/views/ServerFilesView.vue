<script setup lang="ts">
/* global ClipboardEvent, DragEvent, EventTarget, File */
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, shallowRef, useTemplateRef } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, type ButtonInstance } from 'element-plus'
import { APIError } from '../api'
import { errorText, formatTime } from '../format'
import { serverFiles, type ServerDirectory, type ServerFileEntry } from '../server-files-api'
import AppIcon from '../components/AppIcon.vue'
import ListState from '../components/ListState.vue'
import PageHeader from '../components/PageHeader.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'

const { path } = useRoute().query
const initialPath = typeof path === 'string' && path.startsWith('/') ? path : '/'
const directory = shallowRef<ServerDirectory>()
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const uploading = ref(false)
const busy = computed(() => loading.value || saving.value || uploading.value)
const checkedPaths = ref(new Set<string>())
const checkedEntries = computed(() => entries.value.filter(entry => checkedPaths.value.has(entry.path)))
const actionableEntries = computed(() => entries.value.filter(entry => entry.kind !== 'other'))
const clipboard = shallowRef<{ action: 'copy' | 'move'; entries: ServerFileEntry[] }>()
const clipboardType = 'application/x-lpanel-server-files'
let clipboardMarker = ''
const batchResult = shallowRef<{ action: string; succeeded: number; failures: { path: string; reason: string }[] }>()
const fileInput = useTemplateRef('fileInput')
const filePanel = useTemplateRef('filePanel')
const tableRegion = useTemplateRef('tableRegion')
const page = ref(1)
const pageSize = 50
const entries = computed(() => directory.value?.entries ?? [])
const skippedNames = computed(() => directory.value?.skipped_names ?? 0)
const rows = computed(() => entries.value.slice((page.value - 1) * pageSize, page.value * pageSize))
const crumbs = computed(() => {
  const result = [{ name: '/', path: '/' }]
  for (const name of (directory.value?.path ?? initialPath).slice(1).split('/').filter(Boolean)) {
    result.push({ name, path: childPath(result[result.length - 1].path, name) })
  }
  return result
})
const parent = computed(() => crumbs.value.at(-2))
const kindLabels = { dir: '文件夹', file: '文件', link: '链接', other: '其他' }
const byteCount = new Intl.NumberFormat('zh-CN')

function childPath(path: string, name: string) {
  return path === '/' ? `/${name}` : `${path}/${name}`
}

async function loadDirectory(path = directory.value?.path ?? initialPath) {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    const next = await serverFiles.list(path)
    page.value = next.path === directory.value?.path
      ? Math.min(page.value, Math.max(1, Math.ceil(next.entries.length / pageSize)))
      : 1
    if (next.path !== directory.value?.path) {
      checkedPaths.value.clear()
      tableRegion.value?.scrollTo(0, 0)
      if (filePanel.value?.contains(document.activeElement)) {
        void nextTick(() => filePanel.value?.focus({ preventScroll: true }))
      }
    } else {
      checkedPaths.value = new Set(next.entries.filter(entry => checkedPaths.value.has(entry.path)).map(entry => entry.path))
    }
    directory.value = next
  } catch (e) {
    loadError.value = errorText(e, '无法打开文件夹')
  } finally {
    loading.value = false
  }
}
interface UploadItem {
  id: number
  path: string
  size: number
  received: number
  status: 'waiting' | 'uploading' | 'done' | 'failed'
  error?: string
}
const uploadItems = ref<UploadItem[]>([])
const pendingUploads: { item: UploadItem; file: File; directory: string }[] = []
let nextUpload = 0
let leaving = false

onMounted(() => {
  window.addEventListener('paste', pasteFiles)
  void loadDirectory()
})
onUnmounted(() => {
  window.removeEventListener('paste', pasteFiles)
  leaving = true
  pendingUploads.length = 0
})

function chooseFiles() {
  const input = fileInput.value
  if (!input) return
  const files = Array.from(input.files ?? [])
  input.value = ''
  enqueueUploads(files)
}

function allowFileDrop(event: DragEvent) {
  if (!event.dataTransfer?.types.includes('Files')) return
  event.preventDefault()
  event.dataTransfer.dropEffect = directory.value && !loading.value && !saving.value ? 'copy' : 'none'
}

function dropFiles(event: DragEvent) {
  const transfer = event.dataTransfer
  if (!transfer?.types.includes('Files')) return
  event.preventDefault()
  const items = Array.from(transfer.items).filter(item => item.kind === 'file')
  if (items.length === 0) {
    enqueueUploads(transfer.files)
    return
  }
  const files: File[] = []
  let hasDirectory = false
  for (const item of items) {
    if (item.webkitGetAsEntry?.()?.isDirectory) {
      hasDirectory = true
      continue
    }
    const file = item.getAsFile()
    if (file) files.push(file)
  }
  if (hasDirectory) ElMessage.warning('暂时只支持上传文件，请打开文件夹后选择里面的文件')
  enqueueUploads(files)
}

function isTextField(target: EventTarget | null) {
  return target instanceof window.HTMLElement
    && (target.isContentEditable || target.closest('input:not([type="checkbox"]), textarea, select'))
}

function pasteFiles(event: ClipboardEvent) {
  if (dialogOpen.value || event.defaultPrevented || loading.value || saving.value || !directory.value) return
  const target = event.target
  if (isTextField(target)) return
  const transfer = event.clipboardData
  if (!transfer) return
  if (transfer.files.length) {
    event.preventDefault()
    enqueueUploads(transfer.files)
    return
  }
  if (!window.getSelection()?.toString() && target instanceof window.Node && filePanel.value?.contains(target)
      && clipboard.value && clipboardMarker && transfer.getData(clipboardType) === clipboardMarker) {
    event.preventDefault()
    void pasteEntries()
  }
}

function copyOrCut(event: ClipboardEvent, action: 'copy' | 'move') {
  const target = event.target
  if (dialogOpen.value || event.defaultPrevented || busy.value || !checkedEntries.value.length || !event.clipboardData) return
  if (window.getSelection()?.toString() || isTextField(target)) return
  holdEntries(action, checkedEntries.value)
  clipboardMarker = String(Math.random())
  event.clipboardData.setData(clipboardType, clipboardMarker)
  event.preventDefault()
}

function enqueueUploads(files: Iterable<File>) {
  const current = directory.value?.path
  if (!current || loading.value || saving.value) return
  for (const file of files) {
    const item = reactive<UploadItem>({
      id: ++nextUpload,
      path: childPath(current, file.name),
      size: file.size,
      received: 0,
      status: 'waiting',
    })
    uploadItems.value.push(item)
    pendingUploads.push({ item, file, directory: current })
  }
  if (!uploading.value && pendingUploads.length) void processUploads()
}

async function processUploads() {
  uploading.value = true
  const changedDirectories = new Set<string>()
  try {
    for (let next = pendingUploads.shift(); next; next = pendingUploads.shift()) {
      const { item, file, directory: destination } = next
      let id: string | undefined
      item.status = 'uploading'
      try {
        const session = await serverFiles.createUpload(item.path, file.size)
        id = session.id
        while (!leaving && item.received < file.size) {
          const chunk = file.slice(item.received, item.received + session.chunk_size)
          const { received } = await serverFiles.uploadChunk(id, item.received, chunk)
          item.received = received
        }
        if (leaving) {
          await serverFiles.abortUpload(id).catch(() => {})
          return
        }
        await serverFiles.completeUpload(id)
        item.status = 'done'
        changedDirectories.add(destination)
      } catch (e) {
        item.error = e instanceof APIError && e.status === 413 ? '服务器拒绝了上传分片，分片大小超出限制' : errorText(e, '上传失败')
        item.status = 'failed'
        if (id) await serverFiles.abortUpload(id).catch(() => {})
      }
    }
  } finally {
    uploading.value = false
    if (!leaving && directory.value && changedDirectories.has(directory.value.path)) await loadDirectory()
  }
}

function holdEntries(action: 'copy' | 'move', entriesToHold: ServerFileEntry[]) {
  clipboard.value = { action, entries: entriesToHold }
  clipboardMarker = ''
}

async function runBatch(
  action: string,
  targets: ServerFileEntry[],
  perform: (entry: ServerFileEntry) => Promise<unknown>,
) {
  const failures: { path: string; reason: string }[] = []
  const succeeded = new Set<string>()
  for (const entry of targets) {
    try {
      await perform(entry)
      succeeded.add(entry.path)
    } catch (e) {
      failures.push({ path: entry.path, reason: errorText(e, `${action}失败`) })
    }
  }
  batchResult.value = { action, succeeded: succeeded.size, failures }
  return succeeded
}

async function pasteEntries() {
  const transfer = clipboard.value
  const destination = directory.value?.path
  if (!transfer || !destination || busy.value) return
  saving.value = true
  try {
    const move = transfer.action === 'move'
    const succeeded = await runBatch(move ? '移动' : '复制', transfer.entries, entry => serverFiles[transfer.action](entry.path, destination))
    if (move) {
      const remaining = transfer.entries.filter(entry => !succeeded.has(entry.path))
      clipboard.value = remaining.length ? { action: 'move', entries: remaining } : undefined
    }
    await loadDirectory()
  } finally {
    saving.value = false
  }
}

type FileAction = 'mkdir' | 'rename' | 'edit' | 'remove' | 'attributes' | 'compress' | 'extract'
const actions = {
  mkdir: { title: '新建文件夹', submit: '创建', done: '文件夹已创建' },
  rename: { title: '重命名', submit: '保存', done: '名称已修改' },
  edit: { title: '编辑文本', submit: '保存', done: '文件已保存' },
  remove: { title: '确认删除', submit: '删除', done: '已删除' },
  attributes: { title: '修改权限和所有者', submit: '保存', done: '已修改' },
  compress: { title: '压缩为 ZIP', submit: '压缩', done: 'ZIP 压缩包已创建' },
  extract: { title: '解压 ZIP', submit: '解压', done: 'ZIP 已解压' },
}
const target = shallowRef<{ action: FileAction; path: string; entries: ServerFileEntry[] }>()
const dialog = computed(() => target.value && actions[target.value.action])
const dialogOpen = ref(false)
const cancelButton = ref<ButtonInstance>()
const nameInput = useTemplateRef('nameInput')
const modeInput = useTemplateRef('modeInput')
const ownerInput = useTemplateRef('ownerInput')
const attributes = reactive({ applyMode: false, mode: '', applyOwner: false, owner: '', recursive: false })
const includesDirectory = computed(() => target.value?.entries.some(entry => entry.kind === 'dir'))
const modeMixed = computed(() => target.value?.entries.some(entry => entry.mode !== target.value?.entries[0]?.mode))
const ownerMixed = computed(() => target.value?.entries.some(entry => entry.owner !== target.value?.entries[0]?.owner))
const draft = ref('')
const dialogError = ref('')
const readingText = ref(false)
const textReady = ref(false)
const checkedZip = computed(() => checkedEntries.value.length === 1 && isZipFile(checkedEntries.value[0]) ? checkedEntries.value[0] : undefined)
const checkedHasLink = computed(() => checkedEntries.value.some(entry => entry.kind === 'link'))
const nameLabels: Partial<Record<FileAction, string>> = {
  mkdir: '文件夹名称',
  rename: '新名称',
  compress: '压缩包名称',
  extract: '目标文件夹路径',
}

function isZipFile(entry: ServerFileEntry) {
  return entry.kind === 'file' && /\.zip$/i.test(entry.name)
}

function openCompression(items: ServerFileEntry[]) {
  if (!directory.value || !items.length) return
  const random = Array.from(window.crypto.getRandomValues(new Uint32Array(2)), value => value.toString(16).padStart(8, '0')).join('')
  return openAction('compress', directory.value.path, `压缩包-${random}.zip`, items)
}

async function openAction(action: FileAction, path: string, name = '', items: ServerFileEntry[] = []) {
  const selection = { action, path, entries: items }
  target.value = selection
  draft.value = name
  dialogError.value = ''
  textReady.value = false
  readingText.value = action === 'edit'
  dialogOpen.value = true
  if (action !== 'edit') return

  try {
    const result = await serverFiles.text(path)
    if (target.value === selection) {
      draft.value = result.text
      textReady.value = true
    }
  } catch (e) {
    if (target.value === selection) dialogError.value = errorText(e, '无法读取文件')
  } finally {
    if (target.value === selection) readingText.value = false
  }
}

async function submit() {
  const selection = target.value
  if (!selection || saving.value) return
  const { action, path } = selection
  if (action === 'edit' && !textReady.value) return
  const name = action === 'compress' ? draft.value.trim() : draft.value
  if ((action === 'mkdir' || action === 'rename' || action === 'compress') && (!name || name === '.' || name === '..' || name.includes('/'))) {
    dialogError.value = '请输入名称，不能使用 /、. 或 .. 作为路径名称'
    nameInput.value?.focus()
    return
  }
  if (action === 'extract' && !draft.value.startsWith('/')) {
    dialogError.value = '请输入以 / 开头的目标文件夹路径'
    nameInput.value?.focus()
    return
  }
  if (action === 'attributes') {
    if (!attributes.applyMode && !attributes.applyOwner) {
      dialogError.value = '请至少勾选一项要设置的内容'
      return
    }
    if (attributes.applyMode && !/^[0-7]{3,4}$/.test(attributes.mode)) {
      dialogError.value = '请输入三位或四位权限数字（0 到 7）'
      modeInput.value?.focus()
      return
    }
    if (attributes.applyOwner && !attributes.owner.trim()) {
      dialogError.value = '请输入所有者的用户名'
      ownerInput.value?.focus()
      return
    }
  }
  saving.value = true
  dialogError.value = ''
  try {
    switch (action) {
      case 'mkdir': await serverFiles.mkdir(childPath(path, draft.value)); break
      case 'rename': await serverFiles.rename(path, draft.value); break
      case 'edit': await serverFiles.saveText(path, draft.value); break
      case 'compress':
        await serverFiles.compress(path, selection.entries.map(entry => entry.name), /\.zip$/i.test(name) ? name : `${name}.zip`)
        break
      case 'extract':
        await serverFiles.extract(path, draft.value)
        break
      case 'remove':
        await runBatch('删除', selection.entries, entry => serverFiles.remove(entry.path))
        break
      case 'attributes': {
        const mode = attributes.applyMode ? attributes.mode : undefined
        const owner = attributes.applyOwner ? attributes.owner.trim() : undefined
        const recursive = attributes.recursive
        await runBatch('修改', selection.entries, entry => serverFiles.attributes(entry.path, {
          mode,
          owner,
          recursive: recursive && entry.kind === 'dir' ? true : undefined,
        }))
        break
      }
    }
    dialogOpen.value = false
    if (action !== 'remove' && action !== 'attributes') ElMessage.success(actions[action].done)
    await loadDirectory()
  } catch (e) {
    dialogError.value = errorText(e, `${actions[action].submit}失败`)
  } finally {
    saving.value = false
  }
}

function openAttributes(items: ServerFileEntry[]) {
  const first = items[0]
  if (!first) return
  attributes.applyMode = false
  attributes.mode = items.every(entry => entry.mode === first.mode) ? first.mode : ''
  attributes.applyOwner = false
  attributes.owner = items.every(entry => entry.owner === first.owner) ? first.owner : ''
  attributes.recursive = false
  return openAction('attributes', items.length === 1 ? first.path : '', '', items)
}

function menu(entry: ServerFileEntry): MenuItem[] {
  if (entry.kind === 'other') return []
  const items: MenuItem[] = [
    { label: '重命名', icon: 'edit', onSelect: () => { void openAction('rename', entry.path, entry.name) } },
    { label: '复制', icon: 'log', onSelect: () => holdEntries('copy', [entry]) },
    { label: '剪切', icon: 'up', onSelect: () => holdEntries('move', [entry]) },
  ]
  if (entry.kind !== 'link') items.push({ label: '压缩为 ZIP', icon: 'projects', onSelect: () => { void openCompression([entry]) } })
  if (isZipFile(entry)) items.push({ label: '解压 ZIP', icon: 'folder', onSelect: () => { if (directory.value) void openAction('extract', entry.path, directory.value.path) } })
  items.push(
    { label: '修改权限和所有者', icon: 'settings', divided: true, onSelect: () => { void openAttributes([entry]) } },
    { label: '删除', icon: 'trash', danger: true, divided: true, onSelect: () => { void openAction('remove', entry.path, '', [entry]) } },
  )
  return items
}
</script>

<template>
  <PageHeader title="文件管理" description="">
    <input ref="fileInput" type="file" multiple hidden @change="chooseFiles"/>
    <el-button :loading="loading" :disabled="busy" @click="loadDirectory()"><AppIcon v-if="!loading" name="refresh"/>刷新</el-button>
    <el-button :disabled="loading || saving || !directory" @click="fileInput?.click()"><AppIcon name="upload"/>上传文件</el-button>
    <el-button type="primary" :disabled="busy || !directory" @click="directory && openAction('mkdir', directory.path)"><AppIcon name="plus"/>新建文件夹</el-button>
  </PageHeader>

  <div ref="filePanel" class="panel file-panel" tabindex="-1" @copy="copyOrCut($event, 'copy')" @cut="copyOrCut($event, 'move')" @dragover="allowFileDrop" @drop="dropFiles">
    <div class="panel-h file-location">
      <el-button size="small" :disabled="busy || !parent" @click="parent && loadDirectory(parent.path)"><AppIcon name="up"/>上一级</el-button>
      <nav class="file-crumbs" aria-label="当前目录">
        <template v-for="(crumb, index) in crumbs" :key="crumb.path">
          <AppIcon v-if="index" name="chevron"/>
          <span v-if="index === crumbs.length - 1" aria-current="location">{{ crumb.name }}</span>
          <button v-else type="button" class="link" :disabled="busy" @click="loadDirectory(crumb.path)">{{ crumb.name }}</button>
        </template>
      </nav>
    </div>
    <div v-if="directory" class="panel-h file-toolbar" role="group" aria-label="文件操作">
      <el-checkbox
        v-if="actionableEntries.length"
        :model-value="checkedEntries.length === actionableEntries.length"
        :indeterminate="checkedEntries.length > 0 && checkedEntries.length < actionableEntries.length"
        :disabled="busy" @change="checkedPaths = new Set($event ? actionableEntries.map(entry => entry.path) : [])"
      >全选</el-checkbox>
      <span v-if="checkedEntries.length" class="muted">已选 {{ checkedEntries.length }} 项</span>
      <el-button size="small" :disabled="busy || !checkedEntries.length" @click="holdEntries('copy', checkedEntries)">复制</el-button>
      <el-button size="small" :disabled="busy || !checkedEntries.length" @click="holdEntries('move', checkedEntries)">剪切</el-button>
      <el-button size="small" :disabled="busy || !clipboard" @click="pasteEntries">粘贴<span v-if="clipboard">（{{ clipboard.entries.length }} 项）</span></el-button>
      <el-button size="small" :disabled="busy || !checkedEntries.length || checkedHasLink" @click="openCompression(checkedEntries)">压缩</el-button>
      <el-button v-if="checkedZip" size="small" :disabled="busy" @click="openAction('extract', checkedZip.path, directory.path)">解压</el-button>
      <el-button size="small" :disabled="busy || !checkedEntries.length" @click="openAttributes(checkedEntries)">修改权限和所有者</el-button>
      <el-button size="small" type="danger" plain :disabled="busy || !checkedEntries.length" @click="openAction('remove', checkedEntries[0].path, '', checkedEntries)">删除</el-button>
      <span v-if="checkedHasLink" class="muted">取消勾选链接后可压缩。</span>
    </div>
    <el-alert v-if="batchResult" :type="batchResult.failures.length ? 'warning' : 'success'" :role="batchResult.failures.length ? 'alert' : 'status'" show-icon @close="batchResult = undefined">
      <template #title>
        {{ batchResult.action }}成功 {{ batchResult.succeeded }} 项<span v-if="batchResult.failures.length">，失败 {{ batchResult.failures.length }} 项</span>
      </template>
      <ul v-if="batchResult.failures.length" class="file-errors">
        <li v-for="failure in batchResult.failures" :key="failure.path"><span class="file-path">{{ failure.path }}</span>：{{ failure.reason }}</li>
      </ul>
    </el-alert>
    <div v-if="uploadItems.length" class="file-uploads" role="region" aria-label="上传进度">
      <div class="file-toolbar">
        <strong>上传进度</strong>
        <el-button v-if="!uploading" size="small" text @click="uploadItems = []">清除记录</el-button>
      </div>
      <ul>
        <li v-for="item in uploadItems" :key="item.id">
          <span class="file-path">{{ item.path }}</span>
          <span v-if="item.status === 'failed'" class="file-upload-error">{{ item.error }}</span>
          <span v-else-if="item.status === 'done'" role="status">已上传</span>
          <span v-else-if="item.status === 'waiting'">等待上传</span>
          <span v-else-if="item.received === item.size" role="status">正在保存</span>
          <el-progress v-else :percentage="Math.floor(item.received * 100 / item.size)" :aria-label="`上传 ${item.path}`"/>
        </li>
      </ul>
    </div>
    <p v-if="directory" class="file-drop-hint muted">将本机文件拖到此处，或复制后按 Ctrl+V / ⌘V 上传。</p>
    <el-alert v-if="loadError && directory" :title="loadError" role="alert" type="error" :closable="false" show-icon/>
    <el-alert v-if="skippedNames > 0" :title="`有 ${skippedNames} 项因名称编码无法显示。`" type="warning" :closable="false" show-icon/>
    <ListState :loading="!directory && !loadError" :error="directory ? '' : loadError" :empty="!entries.length" icon="folder" :title="skippedNames > 0 ? '没有可显示的项目' : '文件夹为空'" @retry="loadDirectory()">
      <div ref="tableRegion" class="tbl-wrap file-table-region" tabindex="0" role="region" aria-label="文件列表" :aria-busy="loading">
        <table class="file-table" role="table">
          <caption class="sr">文件列表</caption>
          <thead role="rowgroup"><tr role="row"><th role="columnheader" scope="col"><span class="sr">选择</span></th><th role="columnheader" scope="col">名称</th><th role="columnheader" scope="col">类型</th><th role="columnheader" scope="col">大小（字节）</th><th role="columnheader" scope="col">权限</th><th role="columnheader" scope="col">所有者</th><th role="columnheader" scope="col">修改时间</th><th role="columnheader" scope="col">操作</th></tr></thead>
          <tbody role="rowgroup">
            <tr v-for="entry in rows" :key="entry.path" role="row">
              <td role="cell" class="file-checkbox">
                <el-checkbox v-if="entry.kind !== 'other'" :model-value="checkedPaths.has(entry.path)" :aria-label="`选择 ${entry.name}`" :disabled="busy"
                  @change="$event ? checkedPaths.add(entry.path) : checkedPaths.delete(entry.path)"/>
              </td>
              <th role="rowheader" scope="row" class="file-name">
                <AppIcon :name="entry.kind === 'dir' ? 'folder' : entry.kind === 'link' ? 'link' : 'log'"/>
                <button v-if="entry.kind === 'dir'" type="button" class="link" :disabled="busy" @click="loadDirectory(entry.path)">{{ entry.name }}</button>
                <span v-else>{{ entry.name }}</span>
              </th>
              <td role="cell" class="file-kind">{{ kindLabels[entry.kind] }}</td>
              <td role="cell" class="num file-size">{{ entry.kind === 'file' ? byteCount.format(entry.size) : '—' }}<span v-if="entry.kind === 'file'" class="file-size-unit" aria-hidden="true"> 字节</span></td>
              <td role="cell" class="file-mode"><span class="file-attribute-label">权限 </span>{{ entry.mode || '—' }}</td>
              <td role="cell" class="file-owner"><span class="file-attribute-label">所有者 </span>{{ entry.owner || '—' }}</td>
              <td role="cell" class="num muted file-time">{{ formatTime(entry.modified_at) }}</td>
              <td role="cell" class="actions">
                <span v-if="entry.kind !== 'other'" class="row-act">
                  <button v-if="entry.kind === 'file'" type="button" class="link" :aria-label="`编辑 ${entry.name}`" :disabled="busy" @click="openAction('edit', entry.path)">编辑</button>
                  <a v-if="entry.kind === 'file'" :href="serverFiles.downloadURL(entry.path)" :aria-label="`下载 ${entry.name}`" download>下载</a>
                  <RowMenu :label="entry.name" :items="menu(entry)" :disabled="busy"/>
                </span>
                <span v-else>—</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="entries.length > pageSize" class="panel-f file-pages">
        <span class="muted">共 {{ entries.length }} 项</span>
        <el-pagination v-model:current-page="page" :page-size="pageSize" :total="entries.length" :pager-count="5" :disabled="busy" size="small" layout="prev, pager, next" @current-change="tableRegion?.scrollTo({ top: 0 })"/>
      </div>
    </ListState>
  </div>

  <el-dialog
    v-model="dialogOpen" :title="dialog?.title ?? ''" append-to-body align-center
    :width="target?.action === 'edit' ? 'min(720px, calc(100vw - 32px))' : 'min(520px, calc(100vw - 32px))'"
    :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving"
    @opened="target?.action === 'remove' ? cancelButton?.ref?.focus() : nameInput?.focus()"
  >
    <el-alert v-if="dialogError" :title="dialogError" role="alert" type="error" :closable="false" show-icon/>
    <template v-if="target?.action === 'remove'">
      <p>确定永久删除 <code v-if="target.entries.length === 1" class="file-path">{{ target.path }}</code><template v-else>选中的 {{ target.entries.length }} 项</template>？</p>
      <p v-if="includesDirectory">选中文件夹中的所有文件和子文件夹也会永久删除。</p>
    </template>
    <template v-else-if="target?.action === 'edit'">
      <p class="file-path mono">{{ target.path }}</p>
      <p v-if="readingText" class="state" role="status">正在读取文件…</p>
      <el-input v-if="textReady" v-model="draft" type="textarea" :rows="12" :disabled="saving" aria-label="文件内容" spellcheck="false"/>
    </template>
    <el-form v-else label-position="top" @submit.prevent="submit">
      <template v-if="target?.action === 'attributes'">
        <p v-if="target.path" class="file-path mono">{{ target.path }}</p>
        <p v-else>已选 {{ target.entries.length }} 项</p>
        <el-checkbox v-model="attributes.applyMode" :disabled="saving" @change="dialogError = ''">设置权限</el-checkbox>
        <el-form-item label="权限（如 0644）">
          <el-input ref="modeInput" v-model="attributes.mode" name="mode" inputmode="numeric" :placeholder="modeMixed ? '所选项目的权限不同' : undefined"
            :disabled="saving || !attributes.applyMode" @input="dialogError = ''"/>
        </el-form-item>
        <el-checkbox v-model="attributes.applyOwner" :disabled="saving" @change="dialogError = ''">设置所有者</el-checkbox>
        <el-form-item label="所有者（服务器用户名）">
          <el-input ref="ownerInput" v-model="attributes.owner" name="owner" autocapitalize="none" spellcheck="false" :placeholder="ownerMixed ? '所选项目的所有者不同' : undefined"
            :disabled="saving || !attributes.applyOwner" @input="dialogError = ''"/>
        </el-form-item>
        <el-checkbox v-if="includesDirectory" v-model="attributes.recursive" :disabled="saving">同时应用到子文件和子文件夹</el-checkbox>
      </template>
      <template v-else-if="target">
        <p v-if="target.action === 'compress'">将 {{ target.entries.length }} 项压缩到当前文件夹。</p>
        <p v-else-if="target.action === 'extract'" class="file-path mono">{{ target.path }}</p>
        <el-form-item :label="nameLabels[target.action]">
          <el-input ref="nameInput" v-model="draft" :name="target.action" :disabled="saving" @input="dialogError = ''"/>
        </el-form-item>
      </template>
    </el-form>
    <template #footer>
      <el-button ref="cancelButton" :disabled="saving" @click="dialogOpen = false">取消</el-button>
      <el-button :type="target?.action === 'remove' ? 'danger' : 'primary'" :loading="saving" :disabled="target?.action === 'edit' && !textReady" @click="submit">{{ dialog?.submit }}</el-button>
    </template>
  </el-dialog>
</template>
