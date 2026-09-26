import { useI18n } from '../i18n'
import ChangePasswordForm from '../components/ChangePasswordForm'
import FactoryResetPanel from '../components/FactoryResetPanel'

export default function SettingsPage() {
  const { t } = useI18n()
  return (
    <>
      <section className="page-heading">
        <div>
          <h1>{t("Settings")}</h1>
          <p>{t("Manage your portal account and device reset.")}</p>
        </div>
      </section>
      <ChangePasswordForm />
      <FactoryResetPanel />
    </>
  )
}
