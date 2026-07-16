import { Radio } from 'antd'
import { useTranslation } from 'react-i18next'

// DeployMethodSwitch is a thin controlled wrapper around Radio.Group so
// it slots into a Form.Item (value/onChange contract). Docker vs compose
// drives which fields the General tab shows and which Storage/Networking
// editor the storage tab renders.
export function DeployMethodSwitch({
  value,
  onChange,
}: {
  value?: string
  onChange?: (v: string) => void
}) {
  const { t } = useTranslation('apps')
  return (
    <Radio.Group
      value={value}
      onChange={(e) => onChange?.(e.target.value)}
      optionType="button"
      buttonStyle="solid"
    >
      <Radio.Button value="docker">{t('deployMethod.docker')}</Radio.Button>
      <Radio.Button value="compose">{t('deployMethod.compose')}</Radio.Button>
    </Radio.Group>
  )
}
