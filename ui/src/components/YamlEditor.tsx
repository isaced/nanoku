import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'
import { EditorView } from '@codemirror/view'

const editorTheme = EditorView.theme({
  '&': {
    backgroundColor: 'var(--bg-input)',
    color: 'var(--fg)',
    fontSize: '12px',
    borderRadius: '6px',
  },
  '.cm-content': {
    fontFamily:
      "'JetBrains Mono', 'SF Mono', Menlo, Monaco, Consolas, monospace",
    caretColor: 'var(--accent)',
  },
  '.cm-focused': {
    outline: 'none',
  },
  '.cm-focused.cm-editor': {
    boxShadow: '0 0 0 2px var(--accent-soft)',
  },
  '.cm-gutters': {
    backgroundColor: 'transparent',
    color: 'var(--fg-muted)',
    border: 'none',
  },
  '.cm-activeLine': {
    backgroundColor: 'transparent',
  },
  '.cm-activeLineGutter': {
    backgroundColor: 'transparent',
    color: 'var(--fg)',
  },
})

export interface YamlEditorProps {
  value?: string
  onChange?: (value: string) => void
  placeholder?: string
  rows?: number
}

export function YamlEditor({
  value,
  onChange,
  placeholder,
  rows = 10,
}: YamlEditorProps) {
  return (
    <div className="border border-[var(--border)] rounded-md overflow-hidden">
      <CodeMirror
        value={value ?? ''}
        placeholder={placeholder}
        height={`${rows * 1.6}em`}
        theme="light"
        extensions={[yaml(), editorTheme]}
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          highlightActiveLine: true,
          highlightActiveLineGutter: true,
          indentOnInput: true,
          bracketMatching: true,
          closeBrackets: true,
          autocompletion: false,
        }}
        onChange={(next) => onChange?.(next)}
      />
    </div>
  )
}