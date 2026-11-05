// Browser-picked files stay on this device, outside account preferences.
async function mediaStore<T>(action: (store: IDBObjectStore) => IDBRequest<T>, mode: IDBTransactionMode): Promise<T> {
  const db = await new Promise<IDBDatabase>((resolve, reject) => {
    const req = indexedDB.open('office-backgrounds', 1)
    req.onupgradeneeded = () => req.result.createObjectStore('files')
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
  try {
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction('files', mode)
      const req = action(tx.objectStore('files'))
      tx.oncomplete = () => resolve(req.result)
      tx.onabort = () => reject(tx.error)
      tx.onerror = () => reject(tx.error)
    })
  } finally { db.close() }
}

export const storeBackgroundFile = (id: string, file: File) => mediaStore(store => store.put(file, id), 'readwrite')
export const loadBackgroundFile = (id: string) => mediaStore<File | undefined>(store => store.get(id), 'readonly')
export const removeBackgroundFile = (id: string) => mediaStore(store => store.delete(id), 'readwrite')
