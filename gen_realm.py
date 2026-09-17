import os, json

# scripts/keycloak-realm.json
os.makedirs('scripts', exist_ok=True)

realm = {
    "realm": "betting-realm",
    "enabled": True,
    "sslRequired": "none",
    "registrationAllowed": False,
    "loginWithEmailAllowed": False,
    "duplicateEmailsAllowed": False,
    "resetPasswordAllowed": False,
    "editUsernameAllowed": False,
    "bruteForceProtected": True,
    "clients": [
        {
            "clientId": "provider-a",
            "enabled": True,
            "clientAuthenticatorType": "client-secret",
            "secret": "provider-a-secret",
            "serviceAccountsEnabled": True,
            "standardFlowEnabled": False,
            "directAccessGrantsEnabled": False,
            "publicClient": False,
            "protocol": "openid-connect",
            "attributes": {
                "access.token.lifespan": "3600"
            }
        },
        {
            "clientId": "provider-b",
            "enabled": True,
            "clientAuthenticatorType": "client-secret",
            "secret": "provider-b-secret",
            "serviceAccountsEnabled": True,
            "standardFlowEnabled": False,
            "directAccessGrantsEnabled": False,
            "publicClient": False,
            "protocol": "openid-connect",
            "attributes": {
                "access.token.lifespan": "3600"
            }
        },
        {
            "clientId": "betting-service",
            "enabled": True,
            "clientAuthenticatorType": "client-secret",
            "secret": "betting-service-secret",
            "serviceAccountsEnabled": True,
            "standardFlowEnabled": False,
            "directAccessGrantsEnabled": False,
            "publicClient": False,
            "protocol": "openid-connect"
        }
    ],
    "clientScopes": [
        {
            "name": "wagering",
            "description": "Acesso ao servico de apostas",
            "protocol": "openid-connect",
            "attributes": {
                "include.in.token.scope": "true"
            }
        }
    ]
}

with open('scripts/keycloak-realm.json', 'w', encoding='utf-8') as f:
    json.dump(realm, f, indent=2, ensure_ascii=False)

print('scripts/keycloak-realm.json criado')
