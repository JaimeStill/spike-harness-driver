# Azure AI Foundry target

Step 4 checks the direct model client against a cloud target as well as the router. This
runbook sets up an Azure AI Foundry resource with three model deployments and keyless access,
and keeps the connection details out of this public repository.

It follows the precedent herald set: Azure AI Foundry, with managed identity in production.
Here, local development uses the Azure CLI's sign-in in place of the managed identity. Either
way the client sends an Entra ID bearer token, and no API key exists to leak.

Checked against Microsoft Learn on 2026-09-25, and the model choices against eastus2's live
catalog (`az cognitiveservices model list -l eastus2`) on 2026-09-30. The places where the docs
disagree are marked **Verify**.

## What gets created

| Item | Value | Why |
|---|---|---|
| Resource group | `rg-spike-harness` | Holds everything, so cleanup is one delete |
| Resource | kind `AIServices`, SKU `S0`, region `eastus2`, with a custom subdomain | Entra ID auth requires the custom subdomain. eastus2 offers all three models as Global Standard |
| Vision chat | deployment `gpt-5-mini`, version `2025-08-07`, Global Standard | GA until 2027-02-09. The newest small model this subscription has quota for; gpt-6-luna replaces it once quota is granted |
| Embeddings | deployment `text-embedding-3-small`, version `1`, Global Standard | GA until 2028-02-09 (2027-04-15 in Azure Government). No newer OpenAI embedding model exists. 1536 dimensions, with `dimensions` supported. $0.02 per 1M tokens |
| Transcription | deployment `gpt-4o-mini-transcribe`, version `2025-12-15`, Global Standard | GA until 2027-06-15. gpt-transcribe replaces it once quota is granted |

Each deployment is named after its model, because the v1 API takes the deployment name as the
request's `model`.

The models the spike aims for are **gpt-6-luna** (2026-09-22, $0.10 / $0.50 per 1M tokens) and
**gpt-transcribe** (2026-07-28, GA until 2028-02-01, about $0.27 per audio hour). The Visual
Studio subscription this was first run on had a quota of 0 for both, and for every GPT-5.4 and
later model. Step 3 checks the quota first. Request more through the Foundry portal's Quotas
page. Once it's granted, switching is a deployment and a flag (`--vision-model gpt-6-luna`,
`--audio-model gpt-transcribe`), because each pair takes the same request shape.

Models passed over:

- **gpt-4.1-mini** is Legacy in the catalog and Deprecated in the retirement schedule, retiring
  2027-04-14. Microsoft's policy blocks new deployments of a Deprecated version in a
  subscription that never deployed it.
- **gpt-5.6-luna** (2026-07-09, GA until 2028-01-11, $0.20 / $1.20) is the model Azure
  Government offers. The spike aims for gpt-6-luna, and a deployment in Azure Government
  changes only configuration: the model ID, base URL, and token scope. See the last section.
- **whisper** retires 2026-12-15, and the 2025-03-20 versions of the gpt-4o transcribe models
  retire 2026-10-15. Pin versions: a fallback that deploys the older version dies within weeks.
- **Cohere embed-v4** isn't offered in Azure Government.

gpt-5-mini, gpt-5.6-luna, and gpt-6-luna are reasoning models. On `/chat/completions` they take
`max_completion_tokens`, never `max_tokens`. The GPT-5 models reject `temperature` and
`top_p`, and gpt-5.6 needs `reasoning_effort: "none"` alongside `tools`. Microsoft's GPT-6 table
marks `temperature` as supported. The client never sends `max_tokens` or `temperature`, and
llama.cpp accepts `max_completion_tokens` and `reasoning_effort`, so one request shape serves
every target.

The client targets the v1 API, `https://<account>.openai.azure.com/openai/v1`. It has the same
routes as the router's `/v1` (`/chat/completions`, `/embeddings`, `/audio/transcriptions`) and
takes no `api-version`, so one base URL is all the client needs to switch targets.

## Connection details, and keeping them out of the repository

- **The API key is the only real secret.** It grants full access with no role limits. This
  setup never uses it, and step 6 disables it.
- **The endpoint is not a secret.** The account name resolves in public DNS, and it grants
  nothing without a token. It stays out of the repository anyway, along with the subscription
  and tenant IDs, because it tells an attacker where to look.
- **Where the endpoint lives:** a gitignored `.env.azure` at the repository root, loaded into
  the shell before running `clutch`:

  ```bash
  # .env.azure: gitignored; never commit it.
  AZURE_OPENAI_BASE_URL=https://<account>.openai.azure.com/openai/v1
  ```

  ```bash
  set -a; . ./.env.azure; set +a
  ```

- **Where the token comes from:** `clutch --target azure` runs `az account get-access-token`
  each time its cached token nears expiry. The token is held in memory only.
- **If a key is ever needed,** keep it in the OS keyring (`secret-tool store --label=spike-azure
  service spike-azure`) and inject it for a single command. Never write it to a file in the
  checkout.

Guards:

- `.gitignore` covers `.env*`, `secrets*.json`, and `config.*.json`.
- A gitleaks pre-commit hook scans each staged change (step 7).
- GitHub's secret scanning and push protection are on for the repository (step 7).

## 1. Sign in

```bash
az login
az account show --query '{subscription: name, user: user.name}'
```

## 2. Create the resource

Pick a globally unique account name; it becomes the subdomain.

```bash
RG=rg-spike-harness
ACCT=<unique-name>
LOC=eastus2

az group create -n $RG -l $LOC
az cognitiveservices account create -n $ACCT -g $RG -l $LOC \
  --kind AIServices --sku S0 --custom-domain $ACCT --yes
```

## 3. Confirm the models and the quota, then deploy

```bash
az cognitiveservices account list-models -n $ACCT -g $RG \
  | jq -r '.[] | select(.name | test("gpt-5-mini|gpt-6-luna|text-embedding-3-small|transcribe"))
           | [.name, .version, ([.skus[].name] | join(","))] | @tsv'
```

A model the catalog lists can still have no quota in the subscription. A deployment then fails
with `InsufficientQuota` and a limit of 0. Check the limit first, in thousands of tokens per
minute:

```bash
az cognitiveservices usage list -l $LOC \
  | jq -r '.[] | select(.name.value | test("GlobalStandard\\.(gpt-5-mini|gpt-6-luna|text-embedding-3-small|gpt-4o-mini-transcribe|gpt-transcribe)$"))
           | [.name.value, .limit] | @tsv'
```

If the versions differ from the table, use the ones listed, and check that none retires soon:
`az cognitiveservices model list -l $LOC` shows each version's `deprecation.inference` date.

```bash
dep() {
  az cognitiveservices account deployment create -n $ACCT -g $RG \
    --deployment-name "$1" --model-name "$1" --model-version "$2" \
    --model-format OpenAI --sku-name "$3" --sku-capacity "$4"
}
dep gpt-5-mini             2025-08-07 GlobalStandard 10
dep text-embedding-3-small 1          GlobalStandard 10
dep gpt-4o-mini-transcribe 2025-12-15 GlobalStandard 1
# Once their quota is granted:
# dep gpt-6-luna     2026-09-22 GlobalStandard 10
# dep gpt-transcribe 2026-07-28 GlobalStandard 1
```

Capacity is counted in thousands of tokens per minute. Ten is ample for the scenarios.

## 4. Grant yourself access

Assign the Foundry User role (formerly Azure AI User) by its ID, since the role's name is
mid-rename. **Verify:** some pages name Cognitive Services OpenAI User instead. If step 5
returns 401 or 403 after the role has had time to apply, assign that role too.

```bash
az role assignment create \
  --assignee "$(az ad signed-in-user show --query id -o tsv)" \
  --role 53ca6127-db72-4b80-b1b0-d745d6d5456d \
  --scope "$(az cognitiveservices account show -n $ACCT -g $RG --query id -o tsv)"
```

A role takes up to five minutes to apply.

## 5. Smoke tests, keyless

Write the endpoint to `.env.azure` as shown above, then:

```bash
set -a; . ./.env.azure; set +a
TOKEN=$(az account get-access-token --resource https://ai.azure.com --query accessToken -o tsv)
```

**Verify:** the current docs give the scope `https://ai.azure.com`. If the calls below return
401, try `--resource https://cognitiveservices.azure.com`. The scope that works goes to
`clutch --azure-scope`.

Vision:

```bash
base64 -w0 clutch/examples/media/shapes.png \
| jq -n --rawfile img /dev/stdin '{
  model: "gpt-5-mini",
  reasoning_effort: "low",
  messages: [{role: "user", content: [
    {type: "text", text: "Name each shape in this image and its color."},
    {type: "image_url", image_url: {url: ("data:image/png;base64," + $img)}}]}]}' \
| curl -s "$AZURE_OPENAI_BASE_URL/chat/completions" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d @- \
| jq -r '.choices[0].message.content'
```

Embeddings (expect `1536`):

```bash
curl -s "$AZURE_OPENAI_BASE_URL/embeddings" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"model": "text-embedding-3-small", "input": ["How do bees make honey?"]}' \
| jq '.data[0].embedding | length'
```

Transcription:

```bash
curl -s "$AZURE_OPENAI_BASE_URL/audio/transcriptions" \
  -H "Authorization: Bearer $TOKEN" \
  -F file=@clutch/examples/media/phrase.wav \
  -F model=gpt-4o-mini-transcribe | jq -r .text
```

Once gpt-transcribe is deployed, send `model=gpt-transcribe`. **Verify:** Microsoft's docs
don't yet show gpt-transcribe's request fields on the v1 route.

**Verify:** the transcription overview names the v1 route, but the transcription quickstart
shows only the older deployment path. If the v1 route fails, record the response. The older
path is `https://<account>.openai.azure.com/openai/deployments/<deployment>/audio/transcriptions?api-version=2025-04-01-preview`.

## 6. Disable key access

Once the keyless calls work, turn off key access, so the key that exists can't be used:

```bash
az resource update -g $RG -n $ACCT \
  --resource-type Microsoft.CognitiveServices/accounts \
  --set properties.disableLocalAuth=true
```

The change can take minutes or longer to apply. It has applied when a call with the key
returns 401:

```bash
KEY=$(az cognitiveservices account keys list -n $ACCT -g $RG --query key1 -o tsv)
curl -s -o /dev/null -w '%{http_code}\n' "$AZURE_OPENAI_BASE_URL/embeddings" \
  -H "api-key: $KEY" -H 'Content-Type: application/json' \
  -d '{"model": "text-embedding-3-small", "input": ["x"]}'
unset KEY
```

## 7. Repository guards

A gitleaks hook scans each staged change before it commits:

```bash
sudo pacman -S gitleaks
cat > .git/hooks/pre-commit <<'EOF'
#!/bin/sh
exec gitleaks git --pre-commit --staged --redact
EOF
chmod +x .git/hooks/pre-commit
```

On GitHub, confirm that secret scanning and push protection are on (Settings, then Advanced
Security). Both are free on public repositories:

```bash
gh api repos/JaimeStill/spike-harness-driver \
  --jq '.security_and_analysis | {secret_scanning, secret_scanning_push_protection}'
```

## 8. Cost and cleanup

Standard deployments carry no standing charge, and billing is per token or per audio minute. The
scenarios' calls cost well under $0.10 in total.

When the spike no longer needs the target:

```bash
az group delete -n $RG --yes
az cognitiveservices account purge -n $ACCT -g $RG -l $LOC
```

A deleted account is soft-deleted, and purging it releases the name.

## Azure Government and IL6, on paper

- **Endpoints:** Azure Government uses `https://<account>.openai.azure.us`, and the token scope
  `https://cognitiveservices.azure.us/.default`. Run `az cloud set -n AzureUSGovernment` before
  `az login`.
- **Deployment types:** only Data Zone Standard, Standard, and Provisioned. There is no Global
  Standard.
- **Models:** it doesn't offer gpt-6-luna. It offers `gpt-5.6-luna`, which takes the same
  request shape, and `text-embedding-3-small`, both as Data Zone Standard in both regions. A
  client there passes `--vision-model gpt-5.6-luna` and changes no code.
  `text-embedding-3-small` retires there on 2027-04-15, ten months before commercial.
- **No transcription model:** Azure Government offers no whisper, gpt-4o-transcribe, or
  gpt-transcribe. Audio at that level needs Azure Speech or a model the service hosts itself.
- **Secret and Top Secret:** these clouds don't publish their endpoint suffixes, token scope, or
  sign-in authority. The client therefore takes the base URL and the scope from configuration,
  and nothing is fixed to `.com`.
