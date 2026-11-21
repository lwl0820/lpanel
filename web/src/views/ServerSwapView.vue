<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, useTemplateRef } from 'vue'
import type { Operation } from '../api'
import { serverSwap, type SwapEntry, type SwapStatus } from '../server-swap-api'
import { errorText } from '../format'
import { useOperation } from '../useOperation'
import AppIcon from '../components/AppIcon.vue'
import ListState from '../components/ListState.vue'
import OperationBanner from '../components/OperationBanner.vue'
import PageHeader from '../components/PageHeader.vue'

type Edit = { action: 'create' } | { action: 'resize' | 'remove'; entry: SwapEntry }

const status = shallowRef<SwapStatus>()
const loading = ref(true)
const loadError = ref('')
const actionError = ref('')
const submitting = ref(false)
const { operation, label, busy, track } = useOperation(serverSwap.operation)
const locked = computed(() => loading.value || submitting.value || busy.value)
const dialog = useTemplateRef('dialog')
const edit = shallowRef<Edit>({ action: 'create' })
const sizeMiB = ref<number | ''>('')
const maxMiB = 1048576
const bytesPerMiB = 1024 ** 2
const typeLabels = { file: '交换文件', partition: '交换分区', unknown: '未知' }
const actionLabels = { create: '新建交换文件', resize: '调整容量', remove: '删除交换文件' }
const numberFormat = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })

function formatCapacity(bytes: number | null) {
  if (bytes === null) return '未知'
  if (bytes === 0) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const unit = Math.min(units.length - 1, Math.floor(Math.log2(bytes) / 10))
  return `${numberFormat.format(bytes / 1024 ** unit)} ${units[unit]}`
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    status.value = await serverSwap.status()
  } catch (e) {
    status.value = undefined
    loadError.value = errorText(e, '无法读取交换空间')
  } finally {
    loading.value = false
  }
}
let unmounted = false
onBeforeUnmount(() => { unmounted = true })
onMounted(load)

async function change(text: string, success: string, send: () => Promise<Operation>) {
  if (locked.value) return
  submitting.value = true
  actionError.value = ''
  operation.value = null
  try {
    const started = await send()
    if (unmounted) return
    const result = await track(started, text, success)
    if (result) await load()
  } catch (e) {
    actionError.value = errorText(e, '操作失败，请重试')
  } finally {
    submitting.value = false
  }
}

async function openEditor(selection: Edit) {
  edit.value = selection
  sizeMiB.value = selection.action === 'resize' && selection.entry.size_bytes !== null
    ? Math.max(1, Math.round(selection.entry.size_bytes / bytesPerMiB))
    : ''
  await nextTick()
  dialog.value?.showModal()
}

function submitEditor() {
  const selection = edit.value
  const size = Number(sizeMiB.value)
  dialog.value?.close()
  if (selection.action === 'create') {
    return change(actionLabels.create, '交换文件已创建', () => serverSwap.create(size))
  }
  const path = selection.entry.path
  if (selection.action === 'resize') {
    return change(`调整 ${path} 的容量`, '交换文件容量已调整', () => serverSwap.resize(path, size))
  }
  return change(`删除 ${path}`, '交换文件已删除', () => serverSwap.remove(path))
}

function toggle(entry: SwapEntry) {
  const active = !entry.active
  return change(`${active ? '启用' : '停用'} ${entry.path}`, active ? '交换文件已启用' : '交换文件已停用',
    () => serverSwap.setActive(entry.path, active))
}
</script>

<template>
  <PageHeader title="交换空间" description="管理服务器的交换文件，查看交换空间用量。">
    <el-button :disabled="locked" @click="load"><AppIcon name="refresh"/>刷新</el-button>
    <el-button type="primary" :disabled="locked || !status" @click="openEditor({ action: 'create' })"><AppIcon name="plus"/>新建交换文件</el-button>
  </PageHeader>

  <OperationBanner :operation="operation" :label="label" @close="operation = null"/>
  <el-alert v-if="actionError" :title="actionError" role="alert" type="error" :closable="false" show-icon/>

  <section v-if="status" class="panel">
    <div class="panel-h"><h2>整机用量</h2></div>
    <dl class="panel-b kv">
      <dt>已启用容量</dt><dd class="num">{{ formatCapacity(status.total_bytes) }}</dd>
      <dt>已用</dt><dd class="num">{{ formatCapacity(status.used_bytes) }}</dd>
    </dl>
  </section>

  <section class="panel" aria-labelledby="swap-list-heading">
    <div class="panel-h"><h2 id="swap-list-heading">交换项</h2></div>
    <ListState :loading="loading && !status" :error="loadError" :empty="!status?.entries.length" title="暂无交换项" @retry="load">
      <div class="tbl-wrap swap-table-region" tabindex="0" role="region" aria-labelledby="swap-list-heading" :aria-busy="loading">
        <table class="swap-table" role="table">
          <thead role="rowgroup"><tr role="row">
            <th scope="col" role="columnheader">路径或设备</th>
            <th scope="col" role="columnheader">类型</th>
            <th scope="col" role="columnheader">来源</th>
            <th scope="col" role="columnheader">容量</th>
            <th scope="col" role="columnheader">已用</th>
            <th scope="col" role="columnheader">状态</th>
            <th scope="col" role="columnheader">操作</th>
          </tr></thead>
          <tbody role="rowgroup">
            <tr v-for="entry in status?.entries" :key="entry.path" role="row">
              <th scope="row" role="rowheader" class="swap-path">{{ entry.path }}</th>
              <td role="cell"><span class="swap-cell-label" aria-hidden="true">类型</span>{{ typeLabels[entry.type] }}</td>
              <td role="cell"><span class="swap-cell-label" aria-hidden="true">来源</span>{{ entry.managed ? '面板创建' : '面板外创建' }}</td>
              <td role="cell"><span class="swap-cell-label" aria-hidden="true">容量</span><span class="num">{{ formatCapacity(entry.size_bytes) }}</span></td>
              <td role="cell"><span class="swap-cell-label" aria-hidden="true">已用</span><span class="num">{{ formatCapacity(entry.used_bytes) }}</span></td>
              <td role="cell"><span class="swap-cell-label" aria-hidden="true">状态</span><span class="pill" :class="entry.active ? 'ok' : 'idle'">{{ entry.active ? '已启用' : '未启用' }}</span></td>
              <td class="actions" role="cell">
                <div v-if="entry.type === 'file'" class="row-act">
                  <el-button v-if="entry.managed" size="small" :disabled="locked" :aria-label="`调整容量：${entry.path}`" @click="openEditor({ action: 'resize', entry })">调整容量</el-button>
                  <el-button v-if="entry.managed" size="small" :disabled="locked" :aria-label="`${entry.active ? '停用' : '启用'}：${entry.path}`" @click="toggle(entry)">{{ entry.active ? '停用' : '启用' }}</el-button>
                  <el-button type="danger" plain size="small" :disabled="locked" :aria-label="`删除：${entry.path}`" @click="openEditor({ action: 'remove', entry })">删除</el-button>
                </div>
                <span v-else class="muted">仅可查看</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </ListState>
  </section>

  <dialog ref="dialog" class="panel swap-dialog" aria-labelledby="swap-dialog-heading">
    <form @submit.prevent="submitEditor">
      <div class="panel-h"><h2 id="swap-dialog-heading">{{ actionLabels[edit.action] }}</h2></div>
      <div class="panel-b">
        <p v-if="edit.action !== 'create'" class="swap-path">{{ edit.entry.path }}</p>
        <p v-if="edit.action === 'remove'">删除后将释放此文件占用的磁盘空间。</p>
        <div v-else class="swap-capacity">
          <label for="swap-capacity">容量（MiB）</label>
          <el-input id="swap-capacity" v-model="sizeMiB" type="number" min="1" :max="maxMiB" step="1" required aria-describedby="swap-capacity-hint"/>
          <p id="swap-capacity-hint" class="muted">请输入 1 到 1,048,576 之间的整数。1,024 MiB 等于 1 GiB。</p>
        </div>
      </div>
      <div class="panel-f">
        <el-button autofocus @click="dialog?.close()">取消</el-button>
        <el-button :type="edit.action === 'remove' ? 'danger' : 'primary'" native-type="submit">{{ edit.action === 'remove' ? '删除' : edit.action === 'resize' ? '保存' : '创建' }}</el-button>
      </div>
    </form>
  </dialog>
</template>
