import ExitNodeSettingsForm from '../components/ExitNodeSettingsForm'

export default function AccessControlPage() {
  return <>
    <section className="page-heading"><div><h1>Access control</h1><p>Choose an exit node and control local LAN access while using it.</p></div></section>
    <ExitNodeSettingsForm />
  </>
}
