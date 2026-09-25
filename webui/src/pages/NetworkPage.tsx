import LANSettingsForm from '../components/LANSettingsForm'
import TailnetConnectionForm from '../components/TailnetConnectionForm'

export default function NetworkPage() {
  return <>
    <section className="page-heading"><div><h1>Network</h1><p>Manage this device’s tailnet connection and LAN address.</p></div></section>
    <TailnetConnectionForm />
    <LANSettingsForm />
  </>
}
