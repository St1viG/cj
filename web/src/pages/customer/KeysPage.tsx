import type { APIKey, SSHKey } from "@gen/kondukter/api/v1/account_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useApi } from "../../api/ApiProvider";
import { keys } from "../../api/keys";
import { Confirm } from "../../components/Confirm";
import { CopyButton } from "../../components/CopyButton";
import { type Column, DataTable } from "../../components/DataTable";
import { Field } from "../../components/Field";
import { Form } from "../../components/Form";
import { formatTimestamp } from "../../lib/format";
import { newId } from "../../lib/ids";
import { useApiKeys, useSshKeys } from "./queries";

function SshKeys() {
  const { account } = useApi();
  const qc = useQueryClient();
  const query = useSshKeys();
  const refresh = () => qc.invalidateQueries({ queryKey: keys.account.sshKeys });

  const columns: Column<SSHKey>[] = [
    { header: "Name", cell: (k) => k.name },
    { header: "Fingerprint", cell: (k) => <code>{k.fingerprint}</code> },
    { header: "Added", cell: (k) => formatTimestamp(k.createdAt) },
    {
      header: "",
      cell: (k) => (
        <Confirm
          label="Delete"
          message={`Delete ${k.name}? Instances already running keep it.`}
          onConfirm={async () => {
            await account.deleteSSHKey({ id: k.id, idempotencyKey: newId() });
            await refresh();
          }}
        />
      ),
    },
  ];

  return (
    <section>
      <h2>SSH keys</h2>
      <DataTable
        caption="Your SSH keys"
        columns={columns}
        rows={query.data?.sshKeys}
        rowKey={(k) => k.id}
        loading={query.isPending}
        error={query.error}
        onRetry={() => void query.refetch()}
        empty="You have no SSH keys yet. Add one to log in to an instance."
      />
      <Form
        resetOnSuccess
        submitLabel="Add SSH key"
        onSubmit={async (data) => {
          await account.addSSHKey({
            name: String(data.get("name") ?? "").trim(),
            publicKey: String(data.get("public_key") ?? "").trim(),
            idempotencyKey: newId(),
          });
          await refresh();
        }}
      >
        <Field label="Name" name="name" required maxLength={64} />
        <Field
          as="textarea"
          label="Public key"
          name="public_key"
          required
          rows={3}
          placeholder="ssh-ed25519 AAAA… you@laptop"
          hint="The contents of your .pub file."
        />
      </Form>
    </section>
  );
}

function ApiKeys() {
  const { account } = useApi();
  const qc = useQueryClient();
  const query = useApiKeys();
  // The secret exists only in this response; it lives here until dismissed.
  const [created, setCreated] = useState<{ name: string; secret: string } | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: keys.account.apiKeys });

  const columns: Column<APIKey>[] = [
    { header: "Name", cell: (k) => k.name },
    { header: "Key starts with", cell: (k) => <code>{k.prefix}…</code> },
    { header: "Created", cell: (k) => formatTimestamp(k.createdAt) },
    { header: "Last used", cell: (k) => (k.lastUsedAt ? formatTimestamp(k.lastUsedAt) : "never") },
    {
      header: "",
      cell: (k) =>
        k.revokedAt ? (
          `revoked ${formatTimestamp(k.revokedAt)}`
        ) : (
          <Confirm
            label="Revoke"
            message={`Revoke ${k.name}? Anything using it stops working.`}
            onConfirm={async () => {
              await account.revokeAPIKey({ id: k.id, idempotencyKey: newId() });
              await refresh();
            }}
          />
        ),
    },
  ];

  return (
    <section>
      <h2>API keys</h2>
      <p>
        For <code>kctl</code> and the API. A key is shown once, when you create it.
      </p>
      {created && (
        <div role="status">
          <p>
            Your new key <strong>{created.name}</strong>. Copy it now; it is not shown again.
          </p>
          <p>
            <input
              readOnly
              aria-label="New API key"
              value={created.secret}
              size={created.secret.length}
              onFocus={(e) => e.currentTarget.select()}
            />{" "}
            <CopyButton text={created.secret} />{" "}
            <button type="button" onClick={() => setCreated(null)}>
              I have saved it
            </button>
          </p>
        </div>
      )}
      <DataTable
        caption="Your API keys"
        columns={columns}
        rows={query.data?.apiKeys}
        rowKey={(k) => k.id}
        loading={query.isPending}
        error={query.error}
        onRetry={() => void query.refetch()}
        empty="You have no API keys yet."
      />
      <Form
        resetOnSuccess
        submitLabel="Create API key"
        onSubmit={async (data) => {
          const name = String(data.get("name") ?? "").trim();
          // A fresh key every time: replaying one returns the key without its secret.
          const resp = await account.createAPIKey({ name, idempotencyKey: newId() });
          setCreated({ name, secret: resp.secret });
          await refresh();
        }}
      >
        <Field label="Name" name="name" required maxLength={64} />
      </Form>
    </section>
  );
}

/** `/app/keys`: the caller's SSH keys and API keys. */
export function KeysPage() {
  return (
    <section>
      <h1>Keys</h1>
      <SshKeys />
      <ApiKeys />
    </section>
  );
}
