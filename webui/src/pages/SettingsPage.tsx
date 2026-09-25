import ChangePasswordForm from '../components/ChangePasswordForm'

export default function SettingsPage() {
  return (
    <>
      <section className="page-heading">
        <div>
          <h1>Settings</h1>
          <p>Manage your portal account.</p>
        </div>
      </section>
      <ChangePasswordForm />
    </>
  )
}
