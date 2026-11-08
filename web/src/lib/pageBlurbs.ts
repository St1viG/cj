/** One line under each dashboard page's title: what the page is for. Keyed by route pattern. */
export const pageBlurbs: Record<string, string> = {
  "/app/offers": "Everything that can be rented right now. Filter by accelerator, architecture and size, then create an instance from any row.",
  "/app/instances": "Your virtual machines, live. Phases update as they happen, with running time and an estimated cost.",
  "/app/instances/new": "Pick a shape, an image and an SSH key. The scheduler finds the best-fit node and you watch it come up.",
  "/app/instances/:id": "One instance from request to termination: lifecycle, connection details, usage and history.",
  "/app/keys": "SSH keys let you log in to your instances; API keys let `kctl` and your own tools talk to Warden.",
  "/app/usage": "Running time and estimated cost per instance, by the minute.",
  "/app/billing": "Your balance, what it was spent on, and how long it lasts.",
  "/partner/earnings": "What your lent machines earn, per day and per node, and when you are paid.",
  "/partner/sites": "Your datacenter sites, with how many nodes each holds and in what state.",
  "/partner/nodes": "Every machine you lend to Warden: inventory, health and how much is free. Never customer data.",
  "/partner/enroll": "Add a machine: generate a one-time command, run it on the node, and watch it appear.",
  "/partner/utilization": "How much of your lent capacity is in use, per node and over a period.",
  "/admin/nodes": "The whole fleet across partners. Quarantine, drain and check that every agent has caught up.",
  "/admin/instances": "Every customer instance on every node, with the controls to force one to terminate.",
  "/admin/catalog": "What is for sale: offers, shapes, images and regions. Read-only.",
  "/admin/audit": "Every privileged action, newest first, with who did it and what changed.",
};
