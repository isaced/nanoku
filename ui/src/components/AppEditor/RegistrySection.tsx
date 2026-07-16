import { Checkbox, Form, Input } from 'antd'
import { useTranslation } from 'react-i18next'
import type { App as AppType } from '../../lib/types'

// RegistrySection holds the private-registry credential fields. There is
// no fold switch: the Registry tab is its own surface, so the credential
// fields just live here. autoComplete hints keep browsers and password
// managers from pre-filling the empty inputs.
export function RegistrySection({ editing }: { editing: AppType | null }) {
  const { t } = useTranslation('apps')
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-3">
        <Form.Item
          name="registryUrl"
          label={t('registry.url')}
          extra={t('registry.urlExtra')}
          className="!mb-0"
        >
          <Input
            placeholder={t('registry.urlPlaceholder')}
            autoComplete="off"
          />
        </Form.Item>
        <Form.Item
          name="registryUsername"
          label={t('registry.username')}
          className="!mb-0"
        >
          <Input
            placeholder={t('registry.usernamePlaceholder')}
            autoComplete="off"
          />
        </Form.Item>
      </div>
      <Form.Item
        name="registryPassword"
        label={editing?.registryConfigured ? t('registry.passwordNew') : t('registry.password')}
        extra={editing?.registryConfigured ? t('registry.passwordExtra') : undefined}
        className="!mb-0"
      >
        <Input.Password
          placeholder={editing?.registryConfigured ? t('registry.passwordPlaceholderKeep') : t('registry.passwordPlaceholder')}
          autoComplete="new-password"
        />
      </Form.Item>
      {editing?.registryConfigured && (
        <Form.Item name="clearRegistry" valuePropName="checked" className="!mb-0">
          <Checkbox>{t('registry.clear')}</Checkbox>
        </Form.Item>
      )}
    </div>
  )
}
