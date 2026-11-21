<script setup lang="ts">
import { computed, ref } from 'vue'
import ListState from '../components/ListState.vue'
import PageHeader from '../components/PageHeader.vue'
import { formatBytes, formatPercent, formatSampleTime } from '../format'
import { serverMonitor, type MonitorProcess } from '../server-monitor-api'
import { useMonitorPoll } from '../useMonitorPoll'

const { data: processes, error: processError, loading: processesLoading } = useMonitorPoll(serverMonitor.processes, '暂时无法读取进程用量')

const sortColumns = [
  { key: 'cpu_percent', label: 'CPU 使用率', format: formatPercent },
  { key: 'rss_bytes', label: '占用内存', format: formatMemory },
  { key: 'read_bps', label: '磁盘读取', format: formatRate },
  { key: 'write_bps', label: '磁盘写入', format: formatRate },
] as const
type SortKey = (typeof sortColumns)[number]['key']
const sortKey = ref<SortKey>('cpu_percent')
const descending = ref(true)

function formatMemory(value: number | null) {
  return value === null ? '暂不可用' : formatBytes(value)
}

function formatRate(value: number | null) {
  return value === null ? '暂不可用' : `${formatBytes(value)}/秒`
}

function sortBy(key: SortKey) {
  descending.value = sortKey.value === key ? !descending.value : true
  sortKey.value = key
}

const sortedProcesses = computed(() => {
  const key = sortKey.value
  const direction = descending.value ? -1 : 1
  return [...(processes.value?.processes ?? [])].sort((a: MonitorProcess, b: MonitorProcess) => {
    const left = a[key]
    const right = b[key]
    if (left === null) return right === null ? a.pid - b.pid : 1
    if (right === null) return -1
    return (left - right) * direction || a.pid - b.pid
  })
})
</script>

<template>
  <PageHeader title="实时进程" description="点击列标题，可按 CPU、内存或磁盘读写速率排序。">
    <span v-if="processes && !processError" class="muted">采样于 {{ formatSampleTime(processes.at) }}</span>
  </PageHeader>
  <div class="panel">
    <p v-if="processError" class="panel-b muted" role="status">{{ processError }}</p>
    <ListState v-else :loading="processesLoading" error="" :empty="!sortedProcesses.length" title="暂无进程">
      <div class="monitor-process-table" role="region" tabindex="0" aria-label="进程资源用量">
        <table aria-label="进程资源用量">
          <thead>
            <tr>
              <th scope="col">进程</th>
              <th scope="col">进程号</th>
              <th v-for="column in sortColumns" :key="column.key" scope="col"
                  :aria-sort="sortKey === column.key ? (descending ? 'descending' : 'ascending') : undefined">
                <button type="button" @click="sortBy(column.key)">{{ column.label }}<span aria-hidden="true">{{ sortKey === column.key ? (descending ? '↓' : '↑') : '↕' }}</span></button>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="process in sortedProcesses" :key="process.pid">
              <th scope="row">{{ process.name }}</th>
              <td class="num">{{ process.pid }}</td>
              <td v-for="column in sortColumns" :key="column.key" class="num">{{ column.format(process[column.key]) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </ListState>
  </div>
</template>
