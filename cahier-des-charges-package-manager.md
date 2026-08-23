# Cahier des charges — Gestionnaire de paquets Windows compatible Scoop

> **Nom de code : `spm`** (placeholder — à trancher).
> Version 0.2 — révision : l'objectif est un **remplaçant de Scoop**, non un outil interne.

---

## 1. Objectif

Réimplémenter le moteur d'exécution de Scoop, en conservant son format de manifest, pour corriger les limites que son implémentation PowerShell rend structurellement incorrigibles.

**Le produit de Scoop n'est pas son runtime : c'est son corpus de manifests.** `main` et `extras` représentent des milliers de manifests maintenus, avec autoupdate, par des contributeurs tiers. Ce corpus n'est ni réplicable ni à reconstruire. Il est donc consommé tel quel, sans transformation ni fork.

Trajectoire de référence : `fnm` face à `nvm`, `mise` face à `pyenv`. L'écosystème est repris, seul l'exécuteur est réécrit.

## 2. Définition de « en mieux »

Cinq axes, chacun mesurable. Un axe non mesurable ne justifie pas le projet.

| # | Axe | État Scoop | Cible |
|---|---|---|---|
| A1 | **Vitesse** | Parsing PowerShell, téléchargements séquentiels | Ordre de grandeur de gain sur `update` et `status` ; téléchargements parallèles natifs |
| A2 | **Authentification** | Inexistante par dépôt | Native, par hôte, credentials chiffrés |
| A3 | **Reproductibilité** | Aucune | Fichier de verrouillage versionnable, `sync` déterministe |
| A4 | **Dépendances** | `depends` sans contrainte de version | Contraintes de version, résolution explicite, conflits signalés |
| A5 | **Provenance** | Hash seul | Vérification de signature, origine traçable |

### 2.1 Ce qui ne doit pas régresser

Section opposable en revue. Un remplaçant qui régresse sur un seul de ces points n'est pas un remplaçant.

| Réf | Acquis Scoop à préserver |
|---|---|
| NR-01 | Fonctionnement sans droits administrateur, intégralement en espace utilisateur |
| NR-02 | Désinstallation propre : aucune écriture registre, aucun résidu hors répertoire dédié |
| NR-03 | Installations portables, multi-versions, bascule entre versions |
| NR-04 | Conservation des données utilisateur entre versions (`persist`) |
| NR-05 | Buckets tiers utilisables sans configuration particulière |
| NR-06 | Comportement des shims strictement équivalent |
| NR-07 | Réversibilité : un utilisateur peut revenir à Scoop sans réinstaller ses outils |

## 3. Compatibilité manifest — exigence structurante

C'est ici que passe la majorité de l'effort, et c'est le critère qui décide de la viabilité du projet.

| Réf | Exigence | Priorité |
|---|---|---|
| CPT-01 | Décodage du format de manifest Scoop sans transformation ni champ additionnel | Bloquant |
| CPT-02 | Champs polymorphes (`url`, `hash`, `bin` : chaîne \| tableau \| tableau de tableaux) et imbrication sous `architecture.*` | Bloquant |
| CPT-03 | `extract_dir`, `extract_to`, `env_add_path`, `env_set`, `shortcuts`, `persist` | Bloquant |
| CPT-04 | `installer`, `uninstaller`, `pre_install`, `post_install` — exécution des scripts PowerShell par délégation à `pwsh` | Bloquant |
| CPT-05 | Formats d'archive et d'installeur : zip, 7z, MSI, InnoSetup, NSIS | Bloquant |
| CPT-06 | `checkver` et `autoupdate` | Important |
| CPT-07 | Import d'une installation Scoop existante, sans réinstallation des paquets | Important |

> **CPT-04 n'est pas négociable.** Une part significative de `extras` repose sur des scripts PowerShell. Une compatibilité partielle réduirait le corpus utilisable et annulerait l'intérêt du projet. Les scripts sont exécutés par délégation, jamais réinterprétés.

**Critère de viabilité :** un banc de test automatisé installe les 200 manifests les plus utilisés de `main` et `extras`. Sous 95 % de réussite à J3, le projet est réévalué.

## 4. Périmètre

### 4.1 Inclus

Tout ce que couvre Scoop : outils CLI, applications portables, applications à installeur, buckets publics et privés.

### 4.2 Exclu

| Hors périmètre | Motif |
|---|---|
| Installations exigeant les droits administrateur | Identité même de l'outil (NR-01) |
| Drivers et pilotes signés | Hors modèle utilisateur |
| Licences nœud/serveur des IDE vendeurs (Keil MDK, STM32CubeIDE) | Contrainte de licence, non technique — installeurs officiels |
| Linux / macOS | Aucun besoin ; ne pas payer le coût d'abstraction |
| Interface graphique | — |

## 5. Exigences fonctionnelles

### 5.1 Cœur

| Réf | Exigence | Priorité |
|---|---|---|
| EXF-01 | Installer : téléchargement, vérification, extraction, exposition des binaires | Bloquant |
| EXF-02 | Désinstaller sans résidu (NR-02) | Bloquant |
| EXF-03 | Multi-versions et bascule | Bloquant |
| EXF-04 | Lister l'installé avec version, bucket d'origine, état | Bloquant |
| EXF-05 | Mettre à jour un paquet ou l'ensemble | Bloquant |
| EXF-06 | Résoudre les dépendances avec contraintes de version (A4) | Bloquant |
| EXF-07 | Rechercher dans les buckets configurés | Important |
| EXF-08 | Nettoyer les versions obsolètes et le cache | Important |

### 5.2 Reproductibilité (A3)

| Réf | Exigence | Priorité |
|---|---|---|
| EXF-10 | Fichier de verrouillage versionnable : nom, version, bucket, URL résolue, hash, architecture | Bloquant |
| EXF-11 | `sync` installe exactement l'état verrouillé, sans résolution | Bloquant |
| EXF-12 | Détection de divergence avec code retour dédié, exploitable en CI | Bloquant |
| EXF-13 | Format texte, ordre stable, diffable | Bloquant |

### 5.3 Buckets

| Réf | Exigence | Priorité |
|---|---|---|
| EXF-20 | Bucket depuis dépôt Git — mode par défaut, compatibilité avec l'écosystème public | Bloquant |
| EXF-21 | Bucket depuis archive servie par un dépôt d'artefacts, sans Git | Bloquant |
| EXF-22 | Buckets multiples avec ordre de priorité explicite et résolution des collisions de noms | Bloquant |
| EXF-23 | Mise à jour incrémentale des buckets | Important |

### 5.4 Authentification (A2)

| Réf | Exigence | Priorité |
|---|---|---|
| EXF-30 | Authentification par **hôte**, jamais par URL ni inscrite dans un manifest | Bloquant |
| EXF-31 | Bearer (access token) et Basic (utilisateur + token) | Bloquant |
| EXF-32 | Stockage dans le Credential Manager Windows (DPAPI, isolation par utilisateur) | Bloquant |
| EXF-33 | Résolution : variable d'environnement → Credential Manager → anonyme | Bloquant |
| EXF-34 | Gestion des hôtes : ajout, suppression, listage — sans jamais afficher le secret | Bloquant |
| EXF-35 | Aucun credential dans les logs, messages d'erreur ou URLs | Bloquant |

### 5.5 Provenance (A5)

| Réf | Exigence | Priorité |
|---|---|---|
| EXF-40 | Vérification de hash systématique avant extraction | Bloquant |
| EXF-41 | Vérification de signature quand le manifest ou le bucket en fournit une | Important |
| EXF-42 | Traçabilité de l'origine de chaque paquet installé | Important |

## 6. Exigences techniques

| Réf | Exigence | Motif |
|---|---|---|
| EXT-01 | Binaire unique, sans runtime ni dépendance préalable | Bootstrap sur poste neuf |
| EXT-02 | Shim natif compilé | Centaines d'invocations par build ; démarrage d'interpréteur disqualifiant |
| EXT-03 | Téléchargements parallèles, requêtes `Range` natives (A1) | Supprime la dépendance à aria2 |
| EXT-04 | Installation atomique : succès complet ou aucun changement visible | Pas d'état partiel |
| EXT-05 | Ne jamais réinjecter `Authorization` sur redirection cross-domain | Les URLs présignées portent leur auth ; forcer le header fuite le token et provoque un 400 |
| EXT-06 | Décompression déléguée à `7z.exe` et `dark.exe` | Ne pas réimplémenter ; aligné sur Scoop |
| EXT-07 | Codes retour distincts et documentés | Intégration CI |
| EXT-08 | Messages d'erreur actionnables, indiquant la cause et la correction | Axe de qualité différenciant |

### 6.1 Shim

Composant le plus exposé : un défaut ici discrédite l'outil entier.

| Réf | Exigence |
|---|---|
| EXT-20 | Propagation exacte du code de retour |
| EXT-21 | Transmission fidèle des arguments : espaces, guillemets, échappements |
| EXT-22 | Redirection transparente de `stdin`, `stdout`, `stderr`, sans tampon additionnel |
| EXT-23 | Propagation de `Ctrl-C` au processus cible |
| EXT-24 | Shim unique partagé par liens physiques NTFS, cible décrite dans un fichier annexe |
| EXT-25 | Surcoût par invocation négligeable devant le processus appelé |
| EXT-26 | Prise en charge des cibles `.exe`, `.bat`, `.cmd`, `.ps1`, `.jar` |

### 6.2 Contraintes Windows

| Réf | Contrainte | Traitement |
|---|---|---|
| EXT-30 | Binaire en cours d'exécution non remplaçable | Répertoire versionné + jonction `current` ; gérer la jonction elle-même verrouillée |
| EXT-31 | Limite de longueur de chemin | Arborescence courte ; documenter l'activation des chemins longs |
| EXT-32 | Faux positifs antivirus sur binaires récents | Signature du binaire publié ; procédure de liste blanche anticipée |

## 7. Architecture

```
                    ┌──────────────┐
                    │     CLI      │
                    └──────┬───────┘
      ┌────────────────────┼────────────────────┐
      │                    │                    │
┌─────▼─────┐      ┌───────▼───────┐    ┌───────▼───────┐
│  Buckets  │      │   Résolveur   │    │   Lockfile    │
│ git | zip │      │ manifests+dép │    │   lect./écr.  │
└─────┬─────┘      └───────┬───────┘    └───────────────┘
      └──────┬─────────────┘
             │
     ┌───────▼────────┐      ┌──────────────────┐
     │  Téléchargeur  │◄─────┤ Transport auth   │
     │   parallèle    │      │   (par hôte)     │
     └───────┬────────┘      └────────┬─────────┘
             │                        │
     ┌───────▼────────┐      ┌────────▼─────────┐
     │  Installeur    │      │  Coffre creds    │
     │ extract + shim │      │  env | WinCred   │
     └───────┬────────┘      └──────────────────┘
             │
     ┌───────▼────────┐
     │  Pont pwsh     │  (installer.script, pre/post_install)
     └────────────────┘
```

**Invariants d'architecture :**

- L'authentification est une **couche de transport HTTP** clé par hôte, jamais du code dans le téléchargeur. Les manifests restent inchangés et ne peuvent pas fuiter de credential.
- Le fichier de verrouillage est produit par le résolveur, jamais écrit à la main.
- Le shim ignore buckets et réseau : il lit un chemin cible et exécute.
- Le pont `pwsh` est isolé : aucune logique métier n'y transite.

## 8. Choix technologiques

### 8.1 Langage — Go

| Critère | Évaluation |
|---|---|
| Binaire unique, zéro runtime | Natif (EXT-01) |
| Parallélisme des téléchargements | Goroutines — gain A1 à coût faible |
| API Win32 : jonctions, Credential Manager, liens physiques | Couverture mature |
| Vitesse d'itération | Déterminante hors temps plein |
| Lisibilité pour un profil C embarqué | Critère décisif pour la contribution externe |

**Alternatives écartées :**

- **Rust** — supérieur sur le seul décodage polymorphe des manifests (déclaratif contre manuel), mais n'apporte aucun avantage structurel sur un projet dominé par l'I/O réseau et fichier. Barrière d'entrée non justifiée.
- **Python** — bootstrap impossible (l'outil qui installe la toolchain exigerait un runtime préinstallé) ; shim incompatible avec des centaines d'invocations par build.
- **Fork de Scoop** — rebase permanent sur un projet actif, langage subi. Plus coûteux qu'une réécriture à moyen terme.

### 8.2 Coût technique identifié

Le décodage polymorphe des manifests impose un décodage personnalisé sur une dizaine de types en Go. Environ 200 lignes, isolées dans un paquet dédié, couvertes par des tests sur corpus réel. Coût unique, non récurrent.

## 9. Gouvernance

Le risque dominant est le **facteur bus** : un remplaçant de Scoop maintenu par une seule personne est un piège pour ses utilisateurs.

| Réf | Exigence |
|---|---|
| GOV-01 | Projet mené collectivement dès l'origine, jamais présenté fini |
| GOV-02 | Deux personnes au minimum capables de modifier chaque composant |
| GOV-03 | Revue de code systématique |
| GOV-04 | Conventional Commits |
| GOV-05 | Documentation d'architecture dans le dépôt, à jour à chaque jalon |
| GOV-06 | Ouverture du code ; contribution externe possible dès J3 |
| GOV-07 | Réversibilité documentée (NR-07) : retour à Scoop sans réinstallation |

> GOV-07 est aussi l'argument d'adoption : « ça lit les mêmes manifests, tu peux repasser à Scoop quand tu veux » est défendable. « J'ai écrit mon package manager » ne l'est pas.

## 10. Décisions ouvertes

| # | Question | Options | Échéance |
|---|---|---|---|
| D1 | Nom du projet | — | Avant J1 |
| D2 | Format du fichier de verrouillage | TOML \| JSON canonique | Avant J3 |
| D3 | Grammaire des contraintes de version (A4) | SemVer \| ordre lexicographique Scoop étendu | Avant J3 |
| D4 | Répertoire d'installation | Réutiliser `SCOOP` \| répertoire propre + import | Avant J1 |
| D5 | Mécanisme de signature (A5) | minisign \| Sigstore \| Authenticode | Avant J4 |
| D6 | Licence et moment de l'ouverture | — | Avant J3 |

## 11. Lotissement

| Jalon | Contenu | Critère de sortie |
|---|---|---|
| **J0 — Shim** | Shim natif seul, sans gestionnaire | Build CMake/Ninja complet identique à une installation manuelle ; EXT-20 à EXT-26 validés |
| **J1 — Cœur** | Décodage manifest, téléchargement, hash, extraction, install/uninstall/list, bucket Git | 50 manifests `main` s'installent |
| **J2 — Compatibilité** | Pont `pwsh`, MSI/Inno/NSIS, `persist`, `env_*`, `shortcuts`, import Scoop | Banc de test : ≥ 95 % sur 200 manifests `main` + `extras` |
| **J3 — Différenciation** | Lockfile, `sync`, parallélisme, dépendances versionnées, auth, bucket sans Git | A1 à A4 mesurés et documentés |
| **J4 — Publication** | Signature, provenance, documentation, ouverture du code | Utilisable par un tiers sans assistance |

**Ordre imposé : le shim avant le résolveur.** Composant le plus exposé, et celui qui décide de la confiance.

## 12. Critères d'acceptation

1. Banc de test : ≥ 95 % de réussite sur les 200 manifests les plus utilisés de `main` et `extras`.
2. `update` et `status` gagnent au moins un ordre de grandeur sur Scoop, mesuré sur un jeu identique.
3. Un `sync` sur deux postes distincts produit des arborescences aux hashes concordants.
4. Aucun secret en clair sur disque, dans un log ou dans une URL.
5. Aucune régression sur NR-01 à NR-07, vérifiée point par point.
6. Un build CMake/Ninja complet ne montre aucune régression de durée.
7. Une installation Scoop existante s'importe sans réinstaller les paquets.
8. La CI échoue avec un code retour dédié si l'état diverge du fichier de verrouillage.

## 13. Risques

| Risque | Impact | Mitigation |
|---|---|---|
| Compatibilité manifest sous-estimée (§3) | **Critique** | J2 dédié entièrement ; banc de test comme porte de sortie |
| Facteur bus sur un mainteneur unique | Critique | GOV-01 à GOV-03 et GOV-06 ; abandon si non tenu |
| Défaut de shim non détecté | Élevé | J0 isolé, validé sur build réel avant tout autre développement |
| Divergence progressive du format amont | Moyen | Format stable ; banc de test en intégration continue |
| Blocage antivirus | Moyen | Signature du binaire ; procédure IT engagée dès J0 |
| Coût de maintenance permanent | Élevé | Ouverture du code (GOV-06) ; réévaluation formelle en fin de J2 |

> **Point de décision — fin de J2.** Si le banc de test reste sous 95 %, le remplacement complet n'est pas viable : se replier sur un outil complémentaire à Scoop couvrant les seuls axes A2 et A3, à un coût de maintenance sans commune mesure.
