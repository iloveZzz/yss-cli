<script setup lang="ts">
import { computed, shallowRef } from 'vue'
import { createReusableTemplate } from '@vueuse/core'
import { ChevronRight, Folder, FolderOpen } from '@lucide/vue'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import type { AgentFile } from '@/services/workbench-agent'

type Branch = { kind: 'branch'; id: string; label: string; children: Map<string, Entry> }
type Leaf = { kind: 'file'; id: string; file: AgentFile; index: number }
type Entry = Branch | Leaf

const props = defineProps<{ files: AgentFile[]; projectNames: Record<string, string> }>()
defineSlots<{ file(props: { file: AgentFile; index: number }): unknown }>()
const [DefineEntries, Entries] = createReusableTemplate<{ entries: Map<string, Entry>; project?: boolean }>({ inheritAttrs: false })
const openBranches = shallowRef(new Map<string, boolean>())

function branch(parent: Map<string, Entry>, id: string, label: string) {
  const existing = parent.get(id)
  if (existing?.kind === 'branch') return existing
  const entry: Branch = { kind: 'branch', id, label, children: new Map() }
  parent.set(id, entry)
  return entry
}
function branchId(projectId: string, directories: string[]) {
  return JSON.stringify([projectId, 'directory', ...directories])
}

const roots = computed(() => {
  const projects = new Map<string, Entry>()
  for (const [index, file] of props.files.entries()) {
    const { projectId, path } = file
    const directories = path.split('/').slice(0, -1)
    let parent = branch(projects, branchId(projectId, []), props.projectNames[projectId] || projectId)
    for (const [depth, directory] of directories.entries()) {
      parent = branch(parent.children, branchId(projectId, directories.slice(0, depth + 1)), directory)
    }
    const id = JSON.stringify([projectId, 'file', path])
    parent.children.set(id, { kind: 'file', id, file, index })
  }
  return projects
})

function setOpen(id: string, value: boolean) {
  openBranches.value = new Map(openBranches.value).set(id, value)
}
function reveal(file: { projectId: string; path: string }) {
  const directories = file.path.split('/').slice(0, -1)
  const opened = new Map(openBranches.value)
  for (let depth = 0; depth <= directories.length; depth++) {
    opened.set(branchId(file.projectId, directories.slice(0, depth)), true)
  }
  openBranches.value = opened
}
defineExpose({ reveal })
</script>

<template>
  <DefineEntries v-slot="{ entries, project }">
    <ul class="pi-file-tree-level">
      <li v-for="entry in entries.values()" :key="entry.id">
        <Collapsible v-if="entry.kind === 'branch'" :open="openBranches.get(entry.id) ?? !!project"
          @update:open="setOpen(entry.id, $event)">
          <CollapsibleTrigger class="pi-file-tree-folder">
            <ChevronRight class="pi-file-tree-chevron" :size="14" aria-hidden="true" />
            <FolderOpen v-if="openBranches.get(entry.id) ?? project" :size="16" aria-hidden="true" />
            <Folder v-else :size="16" aria-hidden="true" />
            <span>{{ entry.label }}</span>
          </CollapsibleTrigger>
          <CollapsibleContent class="pi-file-tree-children">
            <Entries :entries="entry.children" />
          </CollapsibleContent>
        </Collapsible>
        <slot v-else name="file" :file="entry.file" :index="entry.index" />
      </li>
    </ul>
  </DefineEntries>
  <div class="pi-review-file-list">
    <Entries :entries="roots" :project="true" />
  </div>
</template>

<style scoped>
.pi-file-tree-folder {
  display: flex; align-items: center; gap: 6px; width: 100%; min-height: 38px; padding: 5px 6px;
  border-radius: 4px; text-align: start; font-size: 13px; color: var(--quiet); overflow-wrap: anywhere;
}
.pi-file-tree-folder:hover { background: var(--accent); }
.pi-file-tree-folder svg { flex: none; }
.pi-file-tree-folder[data-state='open'] .pi-file-tree-chevron { transform: rotate(90deg); }
.pi-file-tree-children { margin-inline-start: 12px; padding-inline-start: 8px; border-inline-start: 1px solid var(--border); }
@media (pointer: coarse) { .pi-file-tree-folder { min-height: 44px; } }
@media (width < 600px) { .pi-file-tree-children { margin-inline-start: 6px; padding-inline-start: 5px; } }
</style>
