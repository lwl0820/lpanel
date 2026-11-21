<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, type ButtonInstance } from 'element-plus'
import { images, type LocalImage } from '../images-api'
import { errorText } from '../format'
import AppIcon from '../components/AppIcon.vue'
import ListState from '../components/ListState.vue'
import PageHeader from '../components/PageHeader.vue'

const items = ref<LocalImage[]>([])
const loading = ref(true)
const loadError = ref('')
const pending = ref<LocalImage | 'prune' | null>(null)
const confirmOpen = ref(false)
const cancelButton = ref<ButtonInstance>()
const submitting = ref(false)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    items.value = await images.list()
  } catch (e) {
    loadError.value = errorText(e, '读取镜像失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function ask(target: LocalImage | 'prune') {
  pending.value = target
  confirmOpen.value = true
}

async function confirm() {
  const target = pending.value
  if (!target || submitting.value) return
  submitting.value = true
  confirmOpen.value = false
  try {
    const result = target === 'prune' ? await images.prune() : await images.remove(target.id)
    ElMessage.success(result.message)
  } catch (e) {
    ElMessage.error(errorText(e, target === 'prune' ? '清理镜像失败' : '删除镜像失败'))
  } finally {
    await load()
    submitting.value = false
  }
}
</script>

<template>
  <PageHeader title="镜像管理" description="查看服务器上的本地 Docker 镜像。">
    <el-button :disabled="loading || submitting" @click="load"><AppIcon name="refresh"/>刷新</el-button>
    <el-button type="danger" plain :loading="submitting && pending === 'prune'" :disabled="loading || submitting || !!loadError || !items.length" @click="ask('prune')">
      <AppIcon name="trash"/>清理闲置镜像
    </el-button>
  </PageHeader>
  <div class="panel">
    <ListState :loading="loading" :error="loadError" :empty="!items.length" icon="images" title="暂无本地镜像" @retry="load">
      <div class="tbl-wrap">
        <table class="rtable image-table">
          <thead><tr><th scope="col">镜像</th><th scope="col">大小</th><th scope="col"><span class="sr">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="image in items" :key="image.id">
              <td class="first">
                <span v-if="!image.tags.length" class="name">无标签</span>
                <span v-for="tag in image.tags" :key="tag" class="name">{{ tag }}</span>
                <span class="sub mono">{{ image.id }}</span>
              </td>
              <td data-label="大小" class="num">{{ image.size }}</td>
              <td class="actions">
                <el-button size="small" type="danger" plain :aria-label="`删除镜像 ${image.tags[0] ?? image.id}`" :loading="submitting && pending === image" :disabled="submitting" @click="ask(image)">删除</el-button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </ListState>
  </div>
  <el-dialog v-model="confirmOpen" :title="pending === 'prune' ? '清理闲置镜像' : '删除镜像'" width="min(520px, calc(100vw - 32px))" :close-on-click-modal="false" append-to-body @opened="cancelButton?.ref?.focus()">
    <p v-if="pending === 'prune'">将删除所有未被容器使用的镜像，包括带标签的镜像。正在运行或已停止的容器所使用的镜像会保留。</p>
    <p v-else-if="pending" class="image-confirm-text">确定删除镜像「{{ pending.tags[0] ?? pending.id }}」？</p>
    <template #footer>
      <el-button ref="cancelButton" @click="confirmOpen = false">取消</el-button>
      <el-button type="danger" @click="confirm">{{ pending === 'prune' ? '清理' : '删除' }}</el-button>
    </template>
  </el-dialog>
</template>
