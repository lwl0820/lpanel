<script setup lang="ts">
import { serverMonitor } from '../server-monitor-api'
import { formatBytes, formatPercent, formatSampleTime } from '../format'
import { useMonitorPoll } from '../useMonitorPoll'

defineProps<{ overview?: boolean }>()
const { data, loading, error } = useMonitorPoll(serverMonitor.summary, '暂时无法读取服务器用量')
</script>

<template>
  <section class="panel" aria-labelledby="monitor-summary-heading">
    <div class="panel-h monitor-heading">
      <h2 id="monitor-summary-heading">{{ overview ? '服务器资源' : '实时资源' }}</h2>
      <RouterLink v-if="overview" to="/server/monitor" class="link">查看监控详情</RouterLink>
      <span v-else-if="data && !error" class="muted">采样于 {{ formatSampleTime(data.at) }}</span>
    </div>
    <div v-if="loading" class="state" role="status">正在读取服务器用量…</div>
    <p v-else-if="error" class="panel-b muted" role="status">{{ error }}</p>
    <template v-else-if="data">
      <p v-if="overview" class="monitor-summary-time muted">采样于 {{ formatSampleTime(data.at) }}</p>
      <div v-else class="monitor-processor">
        <span class="muted">处理器</span>
        <strong>{{ data.cpu_model || '型号暂不可用' }}</strong>
        <span v-if="data.cpu_logical_count > 0" class="muted">
          <template v-if="data.cpu_core_count"> {{ data.cpu_core_count }} 个物理核，</template>{{ data.cpu_logical_count }} 个逻辑 CPU
        </span>
      </div>
      <dl class="monitor-metrics">
        <div><dt>CPU 使用率</dt><dd>{{ formatPercent(data.cpu_percent) }}</dd></div>
        <div><dt>已用内存</dt><dd>{{ formatBytes(data.memory_used_bytes) }} <span> / {{ formatBytes(data.memory_total_bytes) }}</span></dd></div>
        <div v-if="!overview">
          <dt>系统平均负载</dt>
          <dd v-if="data.load_average" class="monitor-load-values">
            <div v-for="(minutes, index) in [1, 5, 15]" :key="minutes">
              <span>{{ minutes }} 分钟</span>
              {{ data.load_average[index].toFixed(2) }}
            </div>
          </dd>
          <dd v-else>暂不可用</dd>
        </div>
        <div v-if="overview"><dt>系统盘已用</dt><dd>
          <template v-if="data.root_disk">{{ formatBytes(data.root_disk.used_bytes) }} <span> / {{ formatBytes(data.root_disk.total_bytes) }}</span></template>
          <template v-else>暂不可用</template>
        </dd></div>
      </dl>
    </template>
  </section>
</template>
