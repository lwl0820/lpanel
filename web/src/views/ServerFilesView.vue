<script setup lang="ts">
import { computed, onMounted, ref, shallowRef, useTemplateRef } from 'vue'
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
const fileInput = useTemplateRef('fileInput')
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
    if (next.path !== directory.value?.path) tableRegion.value?.scrollTo(0, 0)
    directory.value = next
  } catch (e) {
    loadError.value = errorText(e, '无法打开文件夹')
  } finally {
    loading.value = false
  }
}
onMounted(() => { void loadDirectory() })

async function upload() {
  const input = fileInput.value
  if (!input) return
  const file = input.files?.[0]
  input.value = ''
  if (!file || !directory.value || busy.value) return
  uploading.value = true
  try {
    await serverFiles.upload(childPath(directory.value.path, file.name), file)
    ElMessage.success('文件已上传')
    await loadDirectory()
  } catch (e) {
    ElMessage.error(e instanceof APIError && e.status === 413 ? '文件太大，无法上传' : errorText(e, '上传失败'))
  } finally {
    uploading.value = false
  }
}

type FileAction = 'mkdir' | 'rename' | 'edit' | 'remove'
const actions = {
  mkdir: { title: '新建文件夹', submit: '创建', done: '文件夹已创建' },
  rename: { title: '重命名', submit: '保存', done: '名称已修改' },
  edit: { title: '编辑文本', submit: '保存', done: '文件已保存' },
  remove: { title: '确认删除', submit: '删除', done: '已删除' },
}
const target = shallowRef<{ action: FileAction; path: string }>()
const dialog = computed(() => target.value && actions[target.value.action])
const dialogOpen = ref(false)
const cancelButton = ref<ButtonInstance>()
const nameInput = useTemplateRef('nameInput')
const draft = ref('')
const dialogError = ref('')
const readingText = ref(false)
const textReady = ref(false)

async function openAction(action: FileAction, path: string, name = '') {
  const selection = { action, path }
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
  if ((action === 'mkdir' || action === 'rename') && (!draft.value || draft.value === '.' || draft.value === '..' || draft.value.includes('/'))) {
    dialogError.value = '请输入名称，不能使用 /、. 或 .. 作为路径名称'
    return
  }
  saving.value = true
  dialogError.value = ''
  try {
    switch (action) {
      case 'mkdir': await serverFiles.mkdir(childPath(path, draft.value)); break
      case 'rename': await serverFiles.rename(path, draft.value); break
      case 'edit': await serverFiles.saveText(path, draft.value); break
      case 'remove': await serverFiles.remove(path); break
    }
    dialogOpen.value = false
    ElMessage.success(actions[action].done)
    await loadDirectory()
  } catch (e) {
    dialogError.value = errorText(e, `${actions[action].submit}失败`)
  } finally {
    saving.value = false
  }
}

function menu(entry: ServerFileEntry): MenuItem[] {
  if (entry.kind === 'other') return []
  return [
    { label: '重命名', icon: 'edit', onSelect: () => { void openAction('rename', entry.path, entry.name) } },
    { label: '删除', icon: 'trash', danger: true, divided: true, onSelect: () => { void openAction('remove', entry.path) } },
  ]
}
</script>

<template>
  <PageHeader title="文件管理" description="">
    <input ref="fileInput" type="file" hidden @change="upload"/>
    <el-button :loading="loading" :disabled="busy" @click="loadDirectory()"><AppIcon v-if="!loading" name="refresh"/>刷新</el-button>
    <el-button :loading="uploading" :disabled="busy || !directory" @click="fileInput?.click()"><AppIcon v-if="!uploading" name="up"/>上传文件</el-button>
    <el-button type="primary" :disabled="busy || !directory" @click="directory && openAction('mkdir', directory.path)"><AppIcon name="plus"/>新建文件夹</el-button>
  </PageHeader>

  <div class="panel file-panel">
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
    <el-alert v-if="loadError && directory" :title="loadError" role="alert" type="error" :closable="false" show-icon/>
    <el-alert v-if="skippedNames > 0" :title="`有 ${skippedNames} 项因名称编码无法显示。`" type="warning" :closable="false" show-icon/>
    <ListState :loading="!directory && !loadError" :error="directory ? '' : loadError" :empty="!entries.length" icon="folder" :title="skippedNames > 0 ? '没有可显示的项目' : '文件夹为空'" @retry="loadDirectory()">
      <div ref="tableRegion" class="tbl-wrap file-table-region" tabindex="0" role="region" aria-label="文件列表" :aria-busy="loading">
        <table class="file-table" role="table">
          <caption class="sr">文件列表</caption>
          <thead role="rowgroup"><tr role="row"><th role="columnheader" scope="col">名称</th><th role="columnheader" scope="col">类型</th><th role="columnheader" scope="col">大小（字节）</th><th role="columnheader" scope="col">修改时间</th><th role="columnheader" scope="col">操作</th></tr></thead>
          <tbody role="rowgroup">
            <tr v-for="entry in rows" :key="entry.path" role="row">
              <th role="rowheader" scope="row" class="file-name">
                <AppIcon :name="entry.kind === 'dir' ? 'folder' : entry.kind === 'link' ? 'link' : 'log'"/>
                <button v-if="entry.kind === 'dir'" type="button" class="link" :disabled="busy" @click="loadDirectory(entry.path)">{{ entry.name }}</button>
                <span v-else>{{ entry.name }}</span>
              </th>
              <td role="cell" class="file-kind">{{ kindLabels[entry.kind] }}</td>
              <td role="cell" class="num file-size">{{ entry.kind === 'file' ? byteCount.format(entry.size) : '—' }}<span v-if="entry.kind === 'file'" class="file-size-unit" aria-hidden="true"> 字节</span></td>
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
    <p v-if="target?.action === 'remove'">确定永久删除 <code class="file-path">{{ target.path }}</code>？</p>
    <template v-else-if="target?.action === 'edit'">
      <p class="file-path mono">{{ target.path }}</p>
      <p v-if="readingText" class="state" role="status">正在读取文件…</p>
      <el-input v-if="textReady" v-model="draft" type="textarea" :rows="12" :disabled="saving" aria-label="文件内容" spellcheck="false"/>
    </template>
    <el-form v-else label-position="top" @submit.prevent="submit">
      <el-form-item :label="target?.action === 'rename' ? '新名称' : '文件夹名称'"><el-input ref="nameInput" v-model="draft" :disabled="saving"/></el-form-item>
    </el-form>
    <template #footer>
      <el-button ref="cancelButton" :disabled="saving" @click="dialogOpen = false">取消</el-button>
      <el-button :type="target?.action === 'remove' ? 'danger' : 'primary'" :loading="saving" :disabled="target?.action === 'edit' && !textReady" @click="submit">{{ dialog?.submit }}</el-button>
    </template>
  </el-dialog>
</template>
