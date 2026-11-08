# Demo: from "Rent a GPU" to a working LLM API

The customer's half of the demo, step by step. You rent the machine on the page (`http://<node>:8080`, signed in as the demo customer), log in to it, put a small model on the GPU, put a gateway in front of it, and call it from your own device. Every command below was run on a real VM from this platform; the numbers are from FED-D.

The machine comes with Ubuntu 24.04, the GTX 1060, the NVIDIA driver, Docker and the NVIDIA container toolkit. Nothing else. You have `sudo` and outbound internet. Besides SSH, **only port 8080 of the machine is reachable from outside**, so the gateway listens there and the model server stays inside on 8081.

## 1. Rent it (on the page)

Press **Rent a GeForce GTX 1060 6GB**. After about 25 s the card shows two lines and a button. Press **Download private key**, then paste the two lines into a terminal on your laptop; they look like this (your port may differ):

```sh
chmod 600 ~/Downloads/kondukter_demo_ed25519
ssh -i ~/Downloads/kondukter_demo_ed25519 -p 40000 ubuntu@100.92.61.78
```

The card also shows the address of port 8080, for step 7 (for example `http://100.92.61.78:40001`).

## 2. Look at the GPU (on the VM, from here on)

```sh
nvidia-smi
```

## 3. Get a model (about 10 s)

```sh
sudo mkdir -p /srv/models && cd /srv/models
sudo curl -fsSLO https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf
```

## 4. Run the model on the GPU (the slow step: a 7 GB container, 6 to 8 minutes here)

```sh
sudo docker run -d --name llm --restart unless-stopped --gpus all -p 127.0.0.1:8081:8080 \
  -v /srv/models:/models ghcr.io/ggml-org/llama.cpp:server-cuda \
  -m /models/qwen2.5-0.5b-instruct-q4_k_m.gguf --host 0.0.0.0 --port 8080 -ngl 99
```

`-p 127.0.0.1:8081:8080` keeps the model server on the machine's own loopback: only the gateway can reach it.

Wait until it answers:

```sh
until curl -sf localhost:8081/health >/dev/null; do sleep 3; done; echo ready
```

## 5. Put a gateway in front of it

Caddy, with an API key and the CORS headers a browser app needs. Change `demo-key` to your own secret.

```sh
sudo tee /srv/Caddyfile >/dev/null <<'EOF'
:8080 {
	@preflight method OPTIONS
	handle @preflight {
		header Access-Control-Allow-Origin *
		header Access-Control-Allow-Headers "Authorization, Content-Type"
		header Access-Control-Allow-Methods "GET, POST, OPTIONS"
		respond 204
	}
	@authorized header Authorization "Bearer {$API_KEY}"
	handle @authorized {
		header Access-Control-Allow-Origin *
		reverse_proxy 127.0.0.1:8081
	}
	handle {
		respond "unauthorized" 401
	}
}
EOF
sudo docker run -d --name gateway --restart unless-stopped --network host -e API_KEY=demo-key \
  -v /srv/Caddyfile:/etc/caddy/Caddyfile:ro caddy:2-alpine
```

## 6. Check it on the machine

```sh
curl -s -o /dev/null -w "no key:   %{http_code}\n" localhost:8080/v1/models
curl -s -o /dev/null -w "with key: %{http_code}\n" localhost:8080/v1/models -H "Authorization: Bearer demo-key"
```

Expect `401`, then `200`.

## 7. Use it from your device

Your device must be on the tailnet. Replace the address with the one on the card.

```sh
curl http://100.92.61.78:40001/v1/chat/completions \
  -H "Authorization: Bearer demo-key" -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"In one sentence: what is a GPU cloud?"}],"max_tokens":60}'
```

It is an OpenAI-style API, so a client library works too: base URL `http://100.92.61.78:40001/v1`, API key `demo-key`, any model name.

```js
const r = await fetch("http://100.92.61.78:40001/v1/chat/completions", {
  method: "POST",
  headers: { Authorization: "Bearer demo-key", "Content-Type": "application/json" },
  body: JSON.stringify({ messages: [{ role: "user", content: "Hello" }], max_tokens: 40 }),
});
console.log((await r.json()).choices[0].message.content);
```

## 8. Give it back

Press **Give it back** on the page. The machine and its disk are deleted; the model and the containers go with it, and the next rental starts from the bare image.

## Notes

- The container image and the model are downloaded again on every rental, since the disk is fresh each time. To save time on a recording, start step 4 as soon as you are logged in and cut the wait.
- Step 4 and 5 use `sudo docker` because the `ubuntu` user is not in the `docker` group.
- If the tab is closed while the machine runs, the page remembers the rental (and the key) for the browser session, but the key file you downloaded is the only copy that survives closing the browser.
