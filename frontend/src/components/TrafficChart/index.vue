<script setup lang="ts">
import { computed, onActivated, onMounted, onUnmounted, ref, useTemplateRef } from 'vue'

import { formatBytes } from '@/utils'

interface Props {
  legend?: string[]
  series: [number[], number[]]
}

const props = withDefaults(defineProps<Props>(), {
  legend: () => ['upload', 'download'],
})

const MAX_HISTORY = 60
const TICK_COUNT = 8
const PLOT_LEFT = 50
const PLOT_TOP = 24
const chart = useTemplateRef<HTMLDivElement>('chart')
const width = ref(200)
const height = ref(214)
const showLines = ref([true, true])
const fillColors = [
  'color-mix(in srgb, var(--chart-upload) 24%, transparent)',
  'color-mix(in srgb, var(--chart-download) 24%, transparent)',
]

const strokeColors = ['var(--chart-upload)', 'var(--chart-download)']

const maxValue = computed(() =>
  Math.max(
    1024,
    ...props.series.flatMap((samples, index) => (showLines.value[index] ? samples : [])),
  ),
)
const bottom = computed(() => height.value - 9)
const right = computed(() => Math.max(PLOT_LEFT, width.value - 6))
const scaleY = (fraction: number) => bottom.value - fraction * (bottom.value - PLOT_TOP)
const ticks = computed(() =>
  Array.from({ length: TICK_COUNT }, (_, index) => {
    const fraction = 1 - index / (TICK_COUNT - 1)
    return { y: scaleY(fraction), label: formatBytes(maxValue.value * fraction) }
  }),
)
const points = computed(() =>
  props.series.map((samples, series) => {
    if (!showLines.value[series]) return ''
    const spacing = (right.value - PLOT_LEFT) / (MAX_HISTORY - 1)
    const offset = samples.length - MAX_HISTORY
    const coordinates = Array.from({ length: MAX_HISTORY }, (_, index) => {
      const x = Math.round(PLOT_LEFT + index * spacing)
      const y = Math.round(scaleY((samples[index + offset] ?? 0) / maxValue.value))
      return x + ',' + y
    })
    return [PLOT_LEFT + ',' + bottom.value, ...coordinates, right.value + ',' + bottom.value].join(
      ' ',
    )
  }),
)

const updateChartSize = () => {
  if (!chart.value?.clientWidth || !chart.value.clientHeight) return
  width.value = chart.value.clientWidth
  height.value = chart.value.clientHeight
}

let resizeObserver: ResizeObserver | undefined
onMounted(() => {
  updateChartSize()
  if (chart.value) {
    resizeObserver = new ResizeObserver(updateChartSize)
    resizeObserver.observe(chart.value)
  }
})

onUnmounted(() => resizeObserver?.disconnect())

onActivated(updateChartSize)
</script>

<template>
  <div ref="chart" class="gui-traffic-chart">
    <div class="chart-legend">
      <button
        v-for="(label, index) in legend"
        :key="index"
        type="button"
        :aria-pressed="showLines[index]"
        :style="{ '--series-color': strokeColors[index] }"
        @click="showLines[index] = !showLines[index]"
      >
        <span class="chart-swatch" aria-hidden="true" />{{ label }}
      </button>
    </div>
    <svg width="100%" height="100%" aria-hidden="true" xmlns="http://www.w3.org/2000/svg">
      <text
        v-for="(tick, index) in ticks"
        :key="index"
        :y="tick.y + 3"
        style="font-size: 9px"
        x="4"
        fill="var(--ui-accent)"
      >
        {{ tick.label }}
      </text>

      <line
        v-for="(tick, index) in ticks"
        :key="index"
        :y1="tick.y"
        :x2="right"
        :y2="tick.y"
        :x1="PLOT_LEFT"
        stroke-dasharray="1 4"
        stroke="var(--chart-grid)"
      />

      <template v-for="(point, index) in points">
        <polyline
          v-if="showLines[index]"
          :key="index"
          :points="point"
          :stroke="strokeColors[index]"
          :fill="fillColors[index]"
          stroke-width="2"
        />
      </template>
    </svg>
  </div>
</template>

<style lang="less" scoped>
.gui-traffic-chart {
  --chart-upload: var(--ui-upload);
  --chart-download: var(--ui-download);
  --chart-grid: color-mix(in srgb, var(--card-color) 70%, transparent);
  position: relative;
  flex: 1;
  min-height: 214px;
  border-radius: 8px;
  background: var(--card-bg);
  svg {
    position: absolute;
    inset: 0;
  }
}
.chart-legend {
  position: absolute;
  z-index: 1;
  inset: 2px 0 auto;
  display: flex;
  justify-content: center;
  gap: 2px;
  button {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    min-height: 20px;
    padding: 2px 5px;
    border: 0;
    border-radius: 4px;
    color: var(--series-color);
    background: transparent;
    font-size: 10px;
    &:hover {
      background: var(--ui-surface-soft);
    }
    &[aria-pressed='false'] {
      color: var(--ui-muted);
      text-decoration: line-through;
    }
  }
}
.chart-swatch {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
body[feature-no-rounded='true'] .gui-traffic-chart {
  border-radius: 0;
}
</style>
