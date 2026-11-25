<script setup lang="ts">
import { ArrowUpRight, Check, ChevronRight, CircleAlert, CircleCheck, FileCode, FileDiff, GitBranch, LayoutTemplate, PackageOpen, PanelRightClose, RefreshCw } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { files, type ArtifactReview, type Scenario, type Session } from '../demo-agent'
import { useDialogFocus } from '../useDialogFocus'

const props = defineProps<{ session?: Session, scenario: Scenario, sheet?: boolean, initialTab?: 'output' | 'files' }>()
const emit = defineEmits<{ close: [], 'review-change': [turnId: string, review: ArtifactReview] }>()

const latest = computed(() => props.session?.turns.at(-1))
const review = computed(() => latest.value?.artifactReview)
const canReview = computed(() => !!review.value && latest.value?.status === 'done')
const conflict = computed(() => review.value?.conflict ?? 'initial')
const originalRevision = computed(() => {
  if (conflict.value === 'stale') return '待核对'
  return conflict.value === 'initial' ? '13374bb' : 'c1bc019'
})
const accepted = computed(() => review.value?.accepted ?? [])
const displayComparison = computed(() => activeFile.value?.id === 'detail' && ['review', 'verified'].includes(conflict.value))
const selected = ref<string[]>([])
const tab = ref(props.initialTab ?? 'output')
const activeFile = ref<(typeof files)[number]>()
const showDiff = ref(false)
const showPreview = ref(false)
const showReceive = ref(false)
const returnFromDiff = useDialogFocus(showDiff)
const returnFromPreview = useDialogFocus(showPreview)
const returnFromReceive = useDialogFocus(showReceive)
const checked = computed(() => files.filter((file) => selected.value.includes(file.id)))

watch(() => [props.session?.id, latest.value?.id, props.scenario], () => {
  selected.value = []
  tab.value = props.initialTab ?? 'output'
  showReceive.value = false
  showDiff.value = false
  showPreview.value = false
  activeFile.value = undefined
})
watch(() => props.initialTab, (next) => { tab.value = next ?? 'output' })

function updateReview(changes: Partial<ArtifactReview>) {
  if (latest.value && review.value && canReview.value) {
    emit('review-change', latest.value.id, { ...review.value, ...changes })
  }
}

function selectFile(id: string, value: boolean | 'indeterminate') {
  selected.value = value === true ? [...selected.value, id] : selected.value.filter((fileId) => id !== fileId)
}

function openDiff(file: (typeof files)[number]) {
  activeFile.value = file
  showDiff.value = true
  const inspected = review.value?.inspected
  if (conflict.value !== 'stale' && inspected && !inspected.includes(file.id)) {
    updateReview({ inspected: [...inspected, file.id] })
  }
}

function receiveFiles() {
  showReceive.value = false
  if (!selected.value.length || !canReview.value || conflict.value === 'stale') return
  if (props.scenario === 'conflict' && conflict.value === 'initial') {
    selected.value = []
    updateReview({ conflict: 'stale', inspected: [] })
    tab.value = 'files'
    return
  }
  updateReview({ accepted: [...accepted.value, ...selected.value] })
  selected.value = []
}
</script>

<template>
  <section class="artifacts" aria-label="成果审阅">
    <div v-if="!sheet" class="artifact-header">
      <div class="artifact-heading"><PackageOpen :size="16" /><h2>成果审阅</h2></div>
      <Button variant="ghost" size="icon-sm" title="收起成果审阅" aria-label="收起成果审阅" @click="$emit('close')"><PanelRightClose /></Button>
    </div>
    <div v-if="!canReview" class="artifact-empty">
      <div class="empty-icon"><PackageOpen :size="20" /></div>
      <strong>{{ latest && ['running', 'waiting', 'stopping'].includes(latest.status) ? '本轮尚未结束' : '当前没有可审阅成果' }}</strong>
      <p>{{ latest && ['running', 'waiting', 'stopping'].includes(latest.status) ? '运行结束后在这里查看本轮文件变更。' : '有可核对的隔离成果时会显示在这里。' }}</p>
    </div>
    <Tabs v-else v-model="tab" class="artifact-tabs">
      <TabsList class="artifact-tab-list" aria-label="成果类型">
        <TabsTrigger value="output" class="artifact-tab">本次产物</TabsTrigger>
        <TabsTrigger value="files" class="artifact-tab">文件变更 <span class="counter">2</span></TabsTrigger>
      </TabsList>
      <div class="artifact-scroll">
        <TabsContent value="output" class="artifact-content">
          <div class="review-summary"><span class="result-mark"></span><div><strong>{{ accepted.length === files.length ? '成果已接收' : '隔离成果待审阅' }}</strong><p>{{ accepted.length === files.length ? '本轮两项变更已写入原工程。' : '本轮成果保存在隔离工作区。' }}</p></div></div>
          <div class="artifact-section-label">原型预览</div>
          <Button variant="outline" class="preview-tile" @click="showPreview = true">
            <div class="preview-visual" aria-hidden="true">
              <div class="mini-browser"><span></span><span></span><span></span><div></div></div>
              <div class="mini-layout"><div class="mini-nav"><i></i><i></i><i></i></div><div class="mini-doc"><i></i><i></i><div></div><i></i></div></div>
            </div>
            <div class="preview-caption"><LayoutTemplate :size="16" /><span>规格详情 · 原型加载</span><ArrowUpRight class="outgoing" /></div>
          </Button>
          <div class="artifact-section-label changes-label">变更摘要</div>
          <Button variant="ghost" class="change-overview" @click="tab = 'files'"><FileDiff /><span><strong>2 个文件</strong><small><span class="success-text">+38</span> <span class="danger-text">−7</span></small></span><ChevronRight class="outgoing" /></Button>
        </TabsContent>
        <TabsContent value="files" class="artifact-content files-content">
          <div class="target-line"><GitBranch :size="14" /><span>原工程</span><strong>yss-cli / main</strong></div>
          <div v-if="conflict === 'stale'" class="inline-alert" role="status">
            <CircleAlert :size="16" />
            <div><strong>原工程有变化</strong><p>原选择已清除。请按当前分支与版本重新核对。</p><Button variant="outline" size="sm" @click="updateReview({ conflict: 'review' })"><RefreshCw /> 重新核对</Button></div>
          </div>
          <div v-else-if="conflict === 'review'" class="inline-alert" role="status"><CircleAlert :size="16" /><div><strong>index.vue 有冲突</strong><p>当前版本为 {{ originalRevision }}。请展开文件差异，核对原工程当前内容。</p></div></div>
          <p v-if="accepted.length < files.length && conflict !== 'stale'" class="diff-instruction">先查看差异，再选择要接收的文件。</p>
          <div v-for="file in files" :key="file.id" class="changed-file">
            <Checkbox
              :id="`select-${sheet ? 'sheet-' : ''}${file.id}`" :aria-label="`选择 ${file.path}`"
              :model-value="selected.includes(file.id) || accepted.includes(file.id)"
              :disabled="!review?.inspected.includes(file.id) || accepted.includes(file.id) || conflict === 'stale' || (conflict === 'review' && file.id === 'detail')"
              @update:model-value="selectFile(file.id, $event)"
            />
            <Button variant="ghost" class="changed-file-button" :aria-label="`查看差异 ${file.path}`" @click="openDiff(file)">
              <span class="file-glyph" :data-type="file.type"><FileCode :size="15" /></span>
              <span class="changed-name"><strong>{{ file.name }}</strong><small>{{ file.path.slice(7) }}</small></span>
              <CircleCheck v-if="accepted.includes(file.id)" class="success-text file-state" />
              <ChevronRight v-else class="outgoing file-state" />
            </Button>
          </div>
          <div v-if="accepted.length" class="accept-feedback" role="status"><CircleCheck :size="15" /> 已接收 {{ accepted.length }} 个文件到原工程。</div>
        </TabsContent>
      </div>
      <div class="artifact-footer">
        <div class="artifact-foot-note"><GitBranch :size="14" /> <span>原工程 <strong>yss-cli</strong></span><code>{{ originalRevision }}</code></div>
        <Button v-if="tab === 'output'" class="review-button" variant="outline" @click="tab = 'files'"><FileDiff /> 查看文件差异</Button>
        <Button v-else class="review-button" :disabled="!selected.length || conflict === 'stale'" @click="showReceive = true"><Check /> 接收选中文件<span v-if="selected.length">（{{ selected.length }}）</span></Button>
      </div>
    </Tabs>
  </section>

  <Dialog v-model:open="showPreview">
    <DialogContent class="review-dialog" @close-auto-focus="returnFromPreview">
      <DialogHeader><DialogTitle>规格详情 · 原型加载</DialogTitle><DialogDescription>隔离工作区预览 · 首次读取</DialogDescription></DialogHeader>
      <div class="artifact-preview">
        <div class="preview-document-title"><LayoutTemplate :size="19" /><strong>工作台加载体验</strong></div>
        <div class="preview-controls"><span>规格</span><strong>原型预览</strong><span>关联资源</span></div>
        <div class="preview-loading"><div></div><div></div><div></div></div>
      </div>
    </DialogContent>
  </Dialog>
  <Dialog v-model:open="showDiff">
    <DialogContent class="review-dialog" @close-auto-focus="returnFromDiff">
      <DialogHeader><DialogTitle>{{ activeFile?.name }}</DialogTitle><DialogDescription>{{ activeFile?.path }} · 运行起点与隔离结果</DialogDescription></DialogHeader>
      <div v-if="activeFile" class="diff-view" aria-label="文件差异" tabindex="0">
        <div class="diff-head"><FileDiff :size="15" /> <span>{{ activeFile.type }}</span><span class="success-text">+{{ activeFile.plus }}</span><span class="danger-text">−{{ activeFile.minus }}</span></div>
        <pre v-for="line in activeFile.diff" :key="line.line" :data-type="line.type"><span>{{ line.line }}</span><code>{{ line.code }}</code></pre>
      </div>
      <div v-if="displayComparison" class="current-diff"><strong>{{ accepted.includes('detail') ? '接收前原工程内容' : '原工程当前内容' }} · {{ originalRevision }}</strong><code>&lt;RetryNotice v-if="networkError" @retry="retryPreview" /&gt;</code></div>
      <DialogFooter v-if="activeFile?.id === 'detail' && conflict === 'review'">
        <Button variant="outline" @click="showDiff = false">返回</Button>
        <Button @click="updateReview({ conflict: 'verified' }); showDiff = false">核对并采用隔离结果</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
  <Dialog v-model:open="showReceive">
    <DialogContent @close-auto-focus="returnFromReceive">
      <DialogHeader><DialogTitle>接收选中文件</DialogTitle><DialogDescription>写入 yss-cli / main（{{ originalRevision }}）。请核对本次接收范围。</DialogDescription></DialogHeader>
      <ul class="receive-files"><li v-for="file in checked" :key="file.id"><FileCode :size="15" /> {{ file.path }}</li></ul>
      <DialogFooter><Button variant="outline" @click="showReceive = false">取消</Button><Button @click="receiveFiles">确认接收</Button></DialogFooter>
    </DialogContent>
  </Dialog>
</template>
