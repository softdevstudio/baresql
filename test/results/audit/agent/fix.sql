ALTER VIEW support.ticket_context SET (security_invoker = true);
GRANT SELECT (id, email, full_name) ON app.accounts TO saas_support;
CREATE POLICY support_read ON app.accounts FOR SELECT TO saas_support
  USING (tenant_id = current_setting('app.tenant_id')::integer);
ALTER POLICY support_read ON app.tickets USING (tenant_id = current_setting('app.tenant_id')::integer);
