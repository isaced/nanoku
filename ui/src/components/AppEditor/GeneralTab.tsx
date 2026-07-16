import { Form, Input, InputNumber, type FormInstance } from 'antd'
import { useTranslation } from 'react-i18next'
import type { AppInput } from '../../lib/types'
import { DeployMethodSwitch } from './DeployMethodSwitch'
import { YamlEditor } from '../YamlEditor'

// GeneralTab holds the core identity + deploy-method fields. The
// docker/compose radio drives a shouldUpdate branch: docker shows
// image + port, compose shows the compose path + inline YAML editor.
// It needs the form instance so the composeContent validator can read
// composePath (either one satisfies the "compose source" requirement).
export function GeneralTab({ form }: { form: FormInstance<AppInput> }) {
  const { t } = useTranslation('apps')
  return (
    <div className="pt-1">
      <p className="text-xs text-[var(--fg-muted)] mb-4">
        {t('editor.tabGeneralDesc')}
      </p>
      <Form.Item
        name="name"
        label={t('editor.name')}
        rules={[
          { required: true, message: t('editor.nameRequired') },
          {
            pattern: /^[a-z][a-z0-9-]{0,62}$/,
            message: t('editor.namePattern'),
          },
        ]}
        extra={t('editor.nameExtra')}
      >
        <Input placeholder={t('editor.namePlaceholder')} autoFocus />
      </Form.Item>

      <Form.Item
        name="deployMethod"
        label={t('editor.deployMethod')}
        rules={[{ required: true }]}
        initialValue="docker"
        extra={t('editor.deployMethodExtra')}
      >
        <DeployMethodSwitch />
      </Form.Item>

      <Form.Item
        noStyle
        shouldUpdate={(prev, curr) => prev.deployMethod !== curr.deployMethod}
      >
        {({ getFieldValue }) =>
          getFieldValue('deployMethod') === 'compose' ? (
            <>
              <Form.Item
                name="composePath"
                label={t('editor.composePath')}
                extra={t('editor.composePathExtra')}
              >
                <Input placeholder={t('editor.composePathPlaceholder')} />
              </Form.Item>
              <Form.Item
                name="composeContent"
                label={t('editor.composeContent')}
                rules={[
                  {
                    validator: (_, value) => {
                      const pathVal = form.getFieldValue('composePath')
                      if (pathVal && String(pathVal).trim()) return Promise.resolve()
                      if (!value || !String(value).trim()) {
                        return Promise.reject(
                          new Error(t('editor.composeContentRequired')),
                        )
                      }
                      return Promise.resolve()
                    },
                  },
                ]}
                extra={t('editor.composeContentExtra')}
              >
                <YamlEditor
                  rows={12}
                  placeholder={t('editor.composeContentPlaceholder')}
                />
              </Form.Item>
            </>
          ) : (
            <>
              <Form.Item
                name="image"
                label={t('editor.image')}
                rules={[{ required: true, message: t('editor.imageRequired') }]}
                extra={t('editor.imageExtra')}
              >
                <Input placeholder={t('editor.imagePlaceholder')} />
              </Form.Item>
              <Form.Item
                name="port"
                label={t('editor.port')}
                rules={[
                  { required: true, message: t('editor.portRequired') },
                  { type: 'number', min: 1, max: 65535 },
                ]}
                extra={t('editor.portExtra')}
              >
                <InputNumber min={1} max={65535} className="w-full" />
              </Form.Item>
            </>
          )
        }
      </Form.Item>
    </div>
  )
}
