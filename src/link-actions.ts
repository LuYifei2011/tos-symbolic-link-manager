export type LinkIdentity = { path: string; fingerprint: string }

// Snapshot the confirmed identities; stop at the first uncertain or failed mutation.
// Never retry a deletion automatically after a timeout or connection failure.
export async function deleteLinks<T extends LinkIdentity>(items: readonly T[], remove: (item: LinkIdentity) => Promise<unknown>, progress: (count: number) => void = () => {}) {
 const snapshot = items.map(({ path, fingerprint }) => ({ path, fingerprint }))
 let completed = 0
 for (const item of snapshot) {
  try { await remove(item) }
  catch (error) { return { completed, failed: item, error } }
  progress(++completed)
 }
 return { completed, failed: null, error: null }
}
