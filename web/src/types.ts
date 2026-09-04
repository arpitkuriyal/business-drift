export type Session = {
  access_token: string
  access_expires_at: string
  refresh_token: string
  refresh_expires_at: string
}

export type Identity = {
  user_id: string
  organization_id: string
  email: string
  role: 'owner' | 'admin' | 'analyst' | 'viewer'
}

export type Organization = {
  id: string
  name: string
  created_at: string
}

export type Evidence = {
  id: string
  source: string
  fact_type: string
  value: unknown
  observed_at: string
}

export type Finding = {
  id: string
  customer_id: string
  customer_name: string
  rule_name: string
  rule_version: number
  status: 'open' | 'resolved'
  risk: 'low' | 'medium' | 'high'
  title: string
  explanation: string
  first_detected_at: string
  last_detected_at: string
  resolved_at?: string
  evidence?: Evidence[]
}

export type StripeIntegration = {
  id: string
  status: 'active' | 'error' | 'disconnected'
  last_synced_at?: string
  last_error?: string
}

export type HubSpotIntegration = {
  id: string
  status: 'active' | 'error' | 'disconnected'
  last_synced_at?: string
  last_error?: string
}

export type HubSpotSyncResult = {
  companies: number
  matched: number
  findings: number
}
