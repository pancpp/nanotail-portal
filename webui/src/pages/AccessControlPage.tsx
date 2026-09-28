import { useI18n } from '../i18n'
import ExitNodeSettingsCard from '../components/ExitNodeSettingsCard'
import SubnetRoutesSettingsForm from '../components/SubnetRoutesSettingsForm'
import { useEffect } from 'react'
import { useLocation } from 'react-router-dom'
import PeerRelaySettingsForm from '../components/PeerRelaySettingsForm'

export default function AccessControlPage() {
  const { t } = useI18n()
  const { hash } = useLocation()
  useEffect(() => {
    if (hash === '#peer-relay') document.getElementById('peer-relay')?.scrollIntoView({ block: 'start' })
  }, [hash])
  return <>
    <section className="page-heading"><div><h1>{t("Access control")}</h1><p>{t("Configure exit-node, subnet, and peer relay services.")}</p></div></section>
    <ExitNodeSettingsCard />
    <SubnetRoutesSettingsForm />
    <PeerRelaySettingsForm />
  </>
}
