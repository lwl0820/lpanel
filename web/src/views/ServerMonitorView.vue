<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef } from 'vue'
import ListState from '../components/ListState.vue'
import MonitorSummary from '../components/MonitorSummary.vue'
import MonitorTrend from '../components/MonitorTrend.vue'
import PageHeader from '../components/PageHeader.vue'
import { errorText, formatBytes, formatPercent, formatSampleTime, formatTime, usagePercent } from '../format'
import { serverMonitor, type MonitorRange, type MonitorSample } from '../server-monitor-api'
import { useMonitorPoll } from '../useMonitorPoll'

const { data: disks, error: diskError, loading: disksLoading } = useMonitorPoll(serverMonitor.disks, '暂时无法读取磁盘容量')

const day = 86400
const retention = 30 * day
const now = () => Math.floor(Date.now() / 1000)
const latest = ref(now())
const range = ref<MonitorRange>({ from: latest.value - day, to: latest.value })
let rollingDays: number | undefined = 1
let editingRange = false
let historyTimer: number
function dateInput(at: number) {
  const date = new Date(at * 1000)
  return `${formatTime(date.toISOString()).replace(' ', 'T')}:${String(date.getSeconds()).padStart(2, '0')}`
}
const fromInput = ref(dateInput(range.value.from))
const toInput = ref(dateInput(range.value.to))
const rangeError = ref('')
const historyError = ref('')
const historyOutdated = ref(false)
const historyLoading = ref(true)
const samples = shallowRef<MonitorSample[]>([])
const diskMount = ref<string>()
let historyRequest: AbortController | undefined

async function loadHistory(nextRange = range.value, background = false) {
  historyRequest?.abort()
  const request = new AbortController()
  historyRequest = request
  const previous = background ? samples.value : []
  const after = previous.at(-1)?.at
  const query = { from: after === undefined ? nextRange.from : Math.max(nextRange.from, after + 1), to: nextRange.to }
  if (!background) {
    range.value = nextRange
    samples.value = []
    historyLoading.value = true
    historyOutdated.value = false
    historyError.value = ''
  }
  try {
    const result = await serverMonitor.history(query, request.signal)
    if (request.signal.aborted) return
    samples.value = [...previous.filter(s => s.at >= nextRange.from), ...result.samples]
    range.value = nextRange
    historyOutdated.value = false
    historyError.value = ''
    if (background && !editingRange) {
      fromInput.value = dateInput(nextRange.from)
      toInput.value = dateInput(nextRange.to)
    }
  } catch (e) {
    if (request.signal.aborted) return
    if (background) historyOutdated.value = true
    else historyError.value = errorText(e, '暂时无法读取历史用量')
  } finally {
    if (historyRequest === request) {
      historyLoading.value = false
      historyRequest = undefined
    }
  }
}

function selectRange(days: number) {
  latest.value = now()
  const to = latest.value
  fromInput.value = dateInput(to - days * day)
  toInput.value = dateInput(to)
  rangeError.value = ''
  rollingDays = days
  editingRange = false
  void loadHistory({ from: to - days * day, to })
}

function applyRange() {
  latest.value = now()
  const from = new Date(fromInput.value).getTime() / 1000
  const to = new Date(toInput.value).getTime() / 1000
  if (!Number.isInteger(from) || !Number.isInteger(to) || from >= to || to > latest.value || to - from > retention) {
    rangeError.value = '请选择最近 30 天内的时段，结束时间须晚于开始时间。'
    return
  }
  // 表单打开到提交期间，最早可查询的时间会随保留期限前移。
  const retainedFrom = Math.max(from, latest.value - retention)
  if (retainedFrom >= to) {
    rangeError.value = '这个时段已超过 30 天，请重新选择。'
    return
  }
  rangeError.value = ''
  rollingDays = undefined
  editingRange = false
  fromInput.value = dateInput(retainedFrom)
  void loadHistory({ from: retainedFrom, to })
}

onMounted(() => {
  void loadHistory()
  historyTimer = window.setInterval(() => {
    if (rollingDays === undefined || historyRequest) return
    const to = now()
    void loadHistory({ from: to - rollingDays * day, to }, !historyError.value)
  }, 60_000)
})
onUnmounted(() => {
  window.clearInterval(historyTimer)
  historyRequest?.abort()
})

const diskMounts = computed(() => [...new Set(samples.value.flatMap(sample => sample.disks.map(disk => disk.mount)))].sort())
const selectedDisk = computed(() => diskMount.value !== undefined && diskMounts.value.includes(diskMount.value)
  ? diskMount.value
  : diskMounts.value[0])
const cpuPoints = computed(() => samples.value.map(s => ({ at: s.at, percent: s.cpu_percent })))
const memoryPoints = computed(() => samples.value.map(s => {
  const percent = usagePercent(s.memory_used_bytes, s.memory_total_bytes)
  return { at: s.at, percent }
}))
const diskPoints = computed(() => samples.value.map(s => {
  const disk = s.disks.find(d => d.mount === selectedDisk.value)
  const percent = disk ? usagePercent(disk.used_bytes, disk.total_bytes) : null
  return { at: s.at, percent }
}))
</script>

<template>
  <PageHeader title="服务器监控" description="查看整台服务器的资源用量和近期趋势。">
    <RouterLink to="/" class="link">返回概览</RouterLink>
  </PageHeader>
  <MonitorSummary/>

  <section class="panel" aria-labelledby="monitor-disks-heading">
    <div class="panel-h monitor-heading">
      <h2 id="monitor-disks-heading">磁盘容量</h2>
      <span v-if="disks && !diskError" class="muted">采样于 {{ formatSampleTime(disks.at) }}</span>
    </div>
    <p v-if="diskError" class="panel-b muted" role="status">{{ diskError }}</p>
    <ListState v-else :loading="disksLoading" error="" :empty="!disks?.disks.length" title="暂无磁盘容量记录">
      <dl class="monitor-metrics">
        <div v-for="disk in disks?.disks" :key="disk.mount">
          <dt>{{ disk.mount === '/' ? '系统盘（/）' : disk.mount }}</dt>
          <dd>{{ formatPercent(usagePercent(disk.used_bytes, disk.total_bytes)) }}
            <span>{{ formatBytes(disk.used_bytes) }} / {{ formatBytes(disk.total_bytes) }}</span>
          </dd>
        </div>
      </dl>
    </ListState>
  </section>

  <section class="panel" aria-labelledby="monitor-history-heading">
    <div class="panel-h"><h2 id="monitor-history-heading">历史趋势</h2></div>
    <div class="panel-b monitor-history">
      <p class="muted">每分钟记录一次，保留最近 30 天。采样中断处留空。</p>
      <div class="monitor-periods">
        <el-button @click="selectRange(1)">近 24 小时</el-button>
        <el-button @click="selectRange(7)">近 7 天</el-button>
        <el-button @click="selectRange(30)">近 30 天</el-button>
      </div>
      <form class="monitor-range" @input="editingRange = true" @submit.prevent="applyRange">
        <label>开始时间<input v-model="fromInput" type="datetime-local" step="1" required></label>
        <label>结束时间<input v-model="toInput" type="datetime-local" step="1" required></label>
        <el-button native-type="submit" type="primary">查询</el-button>
        <p v-if="rangeError" class="monitor-range-error" role="alert">{{ rangeError }}</p>
      </form>
      <ListState :loading="historyLoading" :error="historyError" :empty="!samples.length" title="所选时段暂无记录"
                 description="可选择其他时段查看。" @retry="loadHistory()">
        <div class="monitor-trends">
          <MonitorTrend title="CPU 使用率" :range="range" :points="cpuPoints"/>
          <MonitorTrend title="内存使用率" :range="range" :points="memoryPoints"/>
          <div v-if="selectedDisk !== undefined" class="monitor-disk-trend">
            <label for="monitor-history-disk">查看磁盘</label>
            <el-select id="monitor-history-disk" :model-value="selectedDisk" @update:model-value="diskMount = $event">
              <el-option v-for="mount in diskMounts" :key="mount" :label="mount === '/' ? '系统盘（/）' : mount" :value="mount"/>
            </el-select>
            <MonitorTrend title="磁盘空间使用率" :range="range" :points="diskPoints"/>
          </div>
        </div>
      </ListState>
      <p v-if="historyOutdated" class="muted" role="status">历史数据暂时无法更新，仍显示上次查询结果。</p>
    </div>
  </section>
</template>
