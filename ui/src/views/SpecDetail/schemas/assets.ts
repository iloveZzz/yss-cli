import type { ArtifactChange, ArtifactFile } from '@/services/workbench-platform'
const rasterMimes = new Set([
  'image/png',
  'image/jpeg',
  'image/webp',
  'image/gif',
  'image/avif',
  'image/bmp',
  'image/x-ms-bmp',
  'image/x-icon',
  'image/vnd.microsoft.icon'
])
function rasterFile(file: ArtifactFile | null): boolean {
  return (
    !!file &&
    (rasterMimes.has(file.mime.toLowerCase()) ||
      /\.(png|jpe?g|webp|gif|avif|bmp|ico)$/i.test(file.path))
  )
}
export function imageAsset(file: ArtifactChange): boolean {
  return rasterFile(file.before) || rasterFile(file.after)
}
