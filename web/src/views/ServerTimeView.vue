<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import type { Operation } from '../api'
import { serverTime as timeAPI, type ServerTimeStatus } from '../server-time-api'
import { errorText } from '../format'
import { waitOperation } from '../operation'
import ListState from '../components/ListState.vue'
import OperationBanner from '../components/OperationBanner.vue'
import PageHeader from '../components/PageHeader.vue'

const status = shallowRef<ServerTimeStatus>()
const zones = shallowRef<string[]>()
const timezone = ref('')
const loading = ref(true)
const submitting = ref(false)
const loadError = ref('')
const zonesError = ref('')
const actionError = ref('')
const result = ref('')
const operation = shallowRef<Operation | null>(null)
const lifetime = new AbortController()
const { signal } = lifetime
let statusRequest: AbortController | undefined
let timer: number

async function loadStatus() {
  const request = new AbortController()
  statusRequest = request
  try {
    const current = await timeAPI.status(request.signal)
    if (request.signal.aborted) return
    status.value = current
    loadError.value = ''
  } catch (e) {
    if (!request.signal.aborted) loadError.value = errorText(e, '无法读取服务器时间')
  } finally {
    if (statusRequest === request) {
      loading.value = false
      statusRequest = undefined
    }
  }
}

async function loadZones() {
  zonesError.value = ''
  try {
    const response = await timeAPI.zones(signal)
    if (!signal.aborted) zones.value = response.zones
  } catch (e) {
    if (!signal.aborted) zonesError.value = errorText(e, '无法读取时区列表')
  }
}

async function change(run: () => Promise<ServerTimeStatus | Operation>, success: string) {
  if (submitting.value || loadError.value) return
  statusRequest?.abort()
  submitting.value = true
  actionError.value = ''
  result.value = ''
  operation.value = null
  try {
    const response = await run()
    if (signal.aborted) return
    if ('id' in response) {
      const completed = await waitOperation(id => timeAPI.operation(id, signal), response, {
        signal,
        onUpdate: value => { operation.value = value },
      })
      if (completed.status === 'success') result.value = success
    } else {
      status.value = response
      result.value = success
    }
  } catch (e) {
    if (signal.aborted) return
    actionError.value = errorText(e, operation.value ? '无法确认同步结果' : '操作失败，请重试')
    operation.value = null
  } finally {
    if (!signal.aborted) {
      await loadStatus()
      submitting.value = false
    }
  }
}

async function saveTimezone() {
  if (!timezone.value || timezone.value === status.value?.timezone) return
  const selection = timezone.value
  await change(async () => {
    const updated = await timeAPI.setTimezone(selection, signal)
    timezone.value = ''
    return updated
  }, '时区已更新')
}

function setAuto(value: string | number | boolean) {
  return change(() => timeAPI.setAuto(value === true, signal), '自动校时设置已更新')
}

function syncNow() {
  return change(() => timeAPI.sync(signal), '本次时间同步成功')
}

onMounted(() => {
  void loadStatus()
  void loadZones()
  timer = window.setInterval(() => {
    if (!statusRequest && !submitting.value) void loadStatus()
  }, 5000)
})
onBeforeUnmount(() => {
  window.clearInterval(timer)
  statusRequest?.abort()
  lifetime.abort()
})
</script>

<template>
  <PageHeader title="时区与时间同步" description="查看服务器当地时间，设置时区和自动校时。"/>

  <ListState :loading="loading" :error="status ? '' : loadError" @retry="loadStatus">
    <template v-if="status">
      <el-alert v-if="loadError" :title="loadError" role="alert" type="error" :closable="false" show-icon/>
      <section class="panel" aria-labelledby="server-clock-title">
        <div class="panel-h"><h2 id="server-clock-title">服务器时间</h2></div>
        <div class="panel-b server-time-settings">
          <dl class="kv">
            <dt>当地时间</dt>
            <dd>
              <time class="server-clock" :datetime="status.server_time">
                {{ status.server_time.slice(0, 19).replace('T', ' ') }}
              </time>
            </dd>
            <dt>当前时区</dt><dd>{{ status.timezone || '未知' }}</dd>
          </dl>

          <el-form label-position="top" @submit.prevent="saveTimezone">
            <el-form-item label="更换时区" for="server-time-zone">
              <div class="server-time-controls">
                <el-select id="server-time-zone" v-model="timezone" name="timezone" filterable
                  placeholder="选择或搜索时区" :disabled="submitting || !!loadError || !zones?.length"
                  :loading="!zones && !zonesError">
                  <el-option v-for="zone in zones" :key="zone" :label="zone" :value="zone"/>
                </el-select>
                <el-button native-type="submit" type="primary"
                  :disabled="submitting || !!loadError || !zones?.length || !timezone || timezone === status.timezone">保存时区</el-button>
              </div>
            </el-form-item>
            <div v-if="zonesError">
              <p role="alert">{{ zonesError }}</p>
              <el-button @click="loadZones">重新加载时区</el-button>
            </div>
          </el-form>
        </div>
      </section>

      <section class="panel" aria-labelledby="server-sync-title">
        <div class="panel-h"><h2 id="server-sync-title">时间同步</h2></div>
        <div class="panel-b server-time-settings">
          <dl class="kv">
            <dt>自动校时</dt>
            <dd class="server-time-controls">
              <el-switch v-if="status.auto_sync !== null || status.can_set_auto"
                :model-value="status.auto_sync === true" aria-label="自动校时"
                :disabled="submitting || !!loadError || !status.can_set_auto || status.auto_sync === null"
                @change="setAuto"/>
              <span>{{ status.auto_sync === null ? (status.can_set_auto ? '状态未知' : '不可用') : status.auto_sync ? '已开启' : '已关闭' }}</span>
            </dd>
            <dt>同步状态</dt>
            <dd>{{ status.synchronized === null ? '未知' : status.synchronized ? '已同步' : '未同步' }}</dd>
          </dl>
          <p v-if="status.message" class="muted">{{ status.message }}</p>
          <p v-else-if="!status.can_sync_now" class="muted">当前无法立即同步。</p>
          <div>
            <el-button :disabled="submitting || !!loadError || !status.can_sync_now"
              @click="syncNow">立即同步</el-button>
          </div>
        </div>
      </section>
      <OperationBanner :operation="operation" label="同步时间" @close="operation = null"/>
      <el-alert v-if="actionError" :title="actionError" role="alert" type="error" :closable="false" show-icon/>
      <div role="status"><p v-if="result" class="banner ok">{{ result }}</p></div>
    </template>
  </ListState>
</template>
