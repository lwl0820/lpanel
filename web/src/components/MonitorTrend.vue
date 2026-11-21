<script setup lang="ts">
import { computed, ref, useTemplateRef, watch, watchEffect } from 'vue'
import { formatPercent, formatTime } from '../format'
import type { MonitorRange } from '../server-monitor-api'

const props = defineProps<{
  title: string
  range: MonitorRange
  points: { at: number; percent: number | null }[]
}>()

const canvas = useTemplateRef('canvas')
const width = ref(0)
const height = 104
const fromLabel = computed(() => formatTime(new Date(props.range.from * 1000).toISOString()))
const toLabel = computed(() => formatTime(new Date(props.range.to * 1000).toISOString()))

watch(canvas, (element, _, onCleanup) => {
  if (!element) return
  const observer = new window.ResizeObserver(([entry]) => { width.value = entry.contentRect.width })
  observer.observe(element)
  onCleanup(() => observer.disconnect())
})
const lastPoint = computed(() => {
  for (let i = props.points.length - 1; i >= 0; i--) {
    if (props.points[i].percent !== null) return props.points[i]
  }
  return undefined
})

watchEffect(() => {
  const element = canvas.value
  const w = width.value
  const { from, to } = props.range
  const points = props.points
  if (!element || !w) return
  const ctx = element.getContext('2d')
  if (!ctx) return
  const pixelRatio = window.devicePixelRatio || 1
  element.width = Math.ceil(w * pixelRatio)
  element.height = Math.ceil(height * pixelRatio)
  ctx.scale(pixelRatio, pixelRatio)
  const colors = window.getComputedStyle(element)
  ctx.strokeStyle = colors.getPropertyValue('--line').trim()
  ctx.beginPath()
  for (const y of [2, height / 2, height - 2]) {
    ctx.moveTo(2, y)
    ctx.lineTo(w - 2, y)
  }
  ctx.stroke()

  ctx.strokeStyle = ctx.fillStyle = colors.getPropertyValue('--accent').trim()
  ctx.lineWidth = 1.5
  ctx.beginPath()
  let previousAt = -Infinity
  for (const { at, percent } of points) {
    if (percent === null) {
      previousAt = -Infinity
      continue
    }
    const x = 2 + (at - from) / (to - from) * (w - 4)
    const y = height - 2 - percent / 100 * (height - 4)
    // 每分钟采一次，允许少量时间偏差，孤立样本用小点标明。
    if (at - previousAt > 90) {
      ctx.moveTo(x, y)
      ctx.fillRect(x - 1.5, y - 1.5, 3, 3)
    } else {
      ctx.lineTo(x, y)
    }
    previousAt = at
  }
  ctx.stroke()
})
</script>

<template>
  <figure class="monitor-trend">
    <figcaption>
      <strong>{{ title }}</strong>
      <span v-if="lastPoint" class="muted">最后记录 {{ formatPercent(lastPoint.percent) }}</span>
    </figcaption>
    <template v-if="lastPoint">
      <div class="monitor-chart">
        <div class="monitor-chart-scale muted" aria-hidden="true"><span>100%</span><span>50%</span><span>0%</span></div>
        <canvas ref="canvas" role="img" :aria-label="`${title}趋势`"></canvas>
      </div>
      <div class="monitor-chart-time muted"><time>{{ fromLabel }}</time><time>{{ toLabel }}</time></div>
    </template>
    <p v-else class="muted">所选时段暂无记录</p>
  </figure>
</template>
