import ChangePasswordForm from '../components/ChangePasswordForm'
import FactoryResetPanel from '../components/FactoryResetPanel'

export default function SettingsPage() {
  return (
    <>
      <section className="page-heading">
        <div>
          <h1>Settings</h1>
          <p>Manage your portal account and device reset.</p>
        </div>
      </section>
      <ChangePasswordForm />
      <FactoryResetPanel />
    </>
  )
}
