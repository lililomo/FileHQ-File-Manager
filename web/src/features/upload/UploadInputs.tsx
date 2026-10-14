import { useEffect, useImperativeHandle, useRef, type Ref } from 'react'

export interface UploadInputsHandle {
  pickFiles: () => void
  pickFolder: () => void
}

/** Hidden file inputs behind "Upload file" / "Upload folder" (SPEC §8.8).
 *  `webkitdirectory` keeps the folder structure in `webkitRelativePath`. */
export function UploadInputs({
  ref,
  onFiles,
  testId = 'upload-input',
}: {
  ref: Ref<UploadInputsHandle>
  onFiles: (files: FileList) => void
  /** data-testid prefix (`-file` / `-folder`); the shell uses another one. */
  testId?: string
}) {
  const fileRef = useRef<HTMLInputElement>(null)
  const folderRef = useRef<HTMLInputElement>(null)
  useImperativeHandle(ref, () => ({
    pickFiles: () => fileRef.current?.click(),
    pickFolder: () => folderRef.current?.click(),
  }))
  useEffect(() => {
    // React has no typed prop for the non-standard attribute
    folderRef.current?.setAttribute('webkitdirectory', '')
    folderRef.current?.setAttribute('directory', '')
  }, [])
  const handle = (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.currentTarget.files
    if (files && files.length > 0) onFiles(files)
    e.currentTarget.value = ''
  }
  return (
    <>
      <input
        ref={fileRef}
        type="file"
        multiple
        hidden
        onChange={handle}
        data-testid={`${testId}-file`}
        tabIndex={-1}
      />
      <input
        ref={folderRef}
        type="file"
        multiple
        hidden
        onChange={handle}
        data-testid={`${testId}-folder`}
        tabIndex={-1}
      />
    </>
  )
}
