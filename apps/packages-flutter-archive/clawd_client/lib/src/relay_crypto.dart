/// E2E encryption helpers for relay connections.
///
/// Protocol: X25519 key exchange → HKDF-SHA256 → ChaCha20-Poly1305 AEAD.
///
/// Wire format — handshake (unencrypted JSON):
///   `{"type":"e2e_hello","pubkey":"<32-byte base64url-nopad>"}`
///
/// Wire format — encrypted frames (JSON):
///   `{"type":"e2e","payload":"<base64url-nopad of: nonce_12 || ciphertext>"}`
///
/// Two direction-specific keys are derived from the shared secret:
///   `key_c2d` (info="clawd-relay-c2d-v1"): used for client→daemon encryption
///   `key_d2c` (info="clawd-relay-d2c-v1"): used for daemon→client encryption
library;

import 'dart:convert';
import 'dart:typed_data';
import 'package:cryptography/cryptography.dart';

// ─── Handshake state ──────────────────────────────────────────────────────────

/// Client-side E2E handshake state — holds the ephemeral keypair until the
/// server responds with its public key.
class RelayE2eHandshake {
  RelayE2eHandshake._(this._keyPair, this.clientPubkeyB64);

  final SimpleKeyPair _keyPair;

  /// Base64url-nopad-encoded 32-byte X25519 client public key.
  /// Send this in the `e2e_hello` message to the server.
  final String clientPubkeyB64;

  /// Generate a fresh ephemeral X25519 keypair.
  ///
  /// Use this when forward-secrecy per connection is desired.
  static Future<RelayE2eHandshake> create() async {
    final keyPair = await X25519().newKeyPair();
    final pubKey = await keyPair.extractPublicKey();
    final b64 = _b64urlNopad(Uint8List.fromList(pubKey.bytes));
    return RelayE2eHandshake._(keyPair, b64);
  }

  /// Derive a stable X25519 keypair from a 32-byte [seed].
  ///
  /// Use this when the same keypair must survive app restarts and
  /// WebSocket reconnects (e.g. providing a stable device identity).
  /// The caller is responsible for persisting [seed] in secure storage
  /// (e.g. `flutter_secure_storage`).  [seed] must be exactly 32 bytes.
  static Future<RelayE2eHandshake> createFromSeed(Uint8List seed) async {
    assert(seed.length == 32, 'E2E seed must be 32 bytes');
    final keyPair = await X25519().newKeyPairFromSeed(seed);
    final pubKey = await keyPair.extractPublicKey();
    final b64 = _b64urlNopad(Uint8List.fromList(pubKey.bytes));
    return RelayE2eHandshake._(keyPair, b64);
  }

  /// Complete the handshake given the server's base64url-encoded public key.
  /// Returns the active [RelayE2eSession].
  Future<RelayE2eSession> complete(String serverPubkeyB64) async {
    final serverBytes = _b64urlDecode(serverPubkeyB64);
    final serverPubKey =
        SimplePublicKey(serverBytes, type: KeyPairType.x25519);

    final shared = await X25519().sharedSecretKey(
      keyPair: _keyPair,
      remotePublicKey: serverPubKey,
    );
    final sharedBytes = await shared.extractBytes();

    // Derive direction-specific keys.
    final sendKey = await _deriveKey(sharedBytes, 'clawd-relay-c2d-v1');
    final recvKey = await _deriveKey(sharedBytes, 'clawd-relay-d2c-v1');

    return RelayE2eSession._(sendKey: sendKey, recvKey: recvKey);
  }

  static Future<SecretKey> _deriveKey(List<int> ikm, String info) async {
    return Hkdf(hmac: Hmac.sha256(), outputLength: 32).deriveKey(
      secretKey: SecretKey(ikm),
      nonce: const [],
      info: utf8.encode(info),
    );
  }
}

// ─── Active session ───────────────────────────────────────────────────────────

/// Active E2E session — encrypts outbound frames and decrypts inbound frames.
class RelayE2eSession {
  RelayE2eSession._({required SecretKey sendKey, required SecretKey recvKey})
      : _sendKey = sendKey,
        _recvKey = recvKey;

  final SecretKey _sendKey;
  final SecretKey _recvKey;
  int _sendCounter = 0;
  int _recvCounter = 0;

  static final _chacha = Chacha20.poly1305Aead();

  /// Encrypt [plaintext] → base64url-nopad( nonce_12 || ciphertext ).
  Future<String> encrypt(String plaintext) async {
    final nonce = _makeNonce(_sendCounter++);
    final box = await _chacha.encrypt(
      utf8.encode(plaintext),
      secretKey: _sendKey,
      nonce: nonce,
    );

    // Assemble: nonce (12) || cipherText (N) || mac (16)
    final payload = Uint8List(
        12 + box.cipherText.length + box.mac.bytes.length);
    payload.setRange(0, 12, nonce);
    payload.setRange(12, 12 + box.cipherText.length, box.cipherText);
    payload.setRange(
        12 + box.cipherText.length, payload.length, box.mac.bytes);

    return _b64urlNopad(payload);
  }

  /// Decrypt base64url-nopad( nonce_12 || ciphertext ) → plaintext.
  Future<String> decrypt(String payloadB64) async {
    final data = Uint8List.fromList(_b64urlDecode(payloadB64));
    if (data.length < 12 + 16) {
      throw Exception('e2e payload too short');
    }

    final nonce = data.sublist(0, 12);
    final cipherText = data.sublist(12, data.length - 16);
    final mac = Mac(data.sublist(data.length - 16));

    // Replay protection: verify nonce counter.
    final expected = _makeNonce(_recvCounter);
    if (!_listEquals(nonce, expected)) {
      throw Exception('e2e nonce mismatch — possible replay attack');
    }
    _recvCounter++;

    final box = SecretBox(cipherText, nonce: nonce, mac: mac);
    final pt = await _chacha.decrypt(box, secretKey: _recvKey);
    return utf8.decode(pt);
  }

  // 12-byte nonce: 8-byte LE counter, bytes 8-11 = 0.
  static Uint8List _makeNonce(int counter) {
    final bytes = Uint8List(12);
    for (int i = 0; i < 8; i++) {
      bytes[i] = (counter >> (i * 8)) & 0xFF;
    }
    return bytes;
  }

  static bool _listEquals(List<int> a, List<int> b) {
    if (a.length != b.length) return false;
    for (int i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }
}

// ─── Base64url helpers ────────────────────────────────────────────────────────

String _b64urlNopad(Uint8List bytes) =>
    base64Url.encode(bytes).replaceAll('=', '');

List<int> _b64urlDecode(String s) {
  // Re-add padding if needed.
  final padded = s.padRight((s.length + 3) & ~3, '=');
  return base64Url.decode(padded);
}
