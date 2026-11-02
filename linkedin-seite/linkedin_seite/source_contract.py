"""LinkedIn wire vocabulary: aus Herstellerdoku, live zu belegen.

One location for endpoint, parameter and field names, including OAuth.
No discovery or alternate metric on schema mismatch. Live acceptance must verify
the aggregate follower total and the period coverage in particular.
"""
API_ROOT = 'https://api.linkedin.com/rest/'
TOKEN_URL = 'https://www.linkedin.com/oauth/v2/accessToken'
AUTHORIZE_URL = 'https://www.linkedin.com/oauth/v2/authorization'
REDIRECT_URI = 'http://localhost:8765/linkedin/callback'
SCOPES = ('rw_organization_admin', 'r_organization_social')
VERSION_HEADER = 'LinkedIn-Version'
PROTOCOL_HEADER = 'X-Restli-Protocol-Version'
PROTOCOL_VERSION = '2.0.0'
PAGE = 'organizationPageStatistics'
FOLLOWERS = 'organizationalEntityFollowerStatistics'
SHARES = 'organizationalEntityShareStatistics'
POSTS = 'posts'
NETWORK = 'networkSizes'
EDGE_TYPE = 'edgeType'
FOLLOWED_BY_MEMBER = 'COMPANY_FOLLOWED_BY_MEMBER'
NETWORK_SIZE = 'firstDegreeSize'
Q = 'q'
ORGANIZATION = 'organization'
ENTITY = 'organizationalEntity'
AUTHOR = 'author'
INTERVALS = 'timeIntervals'
INTERVAL_FORMAT = '(timeGranularityType:DAY,timeRange:(start:{start},end:{end}))'
SHARE_IDS = 'shares'
UGC_IDS = 'ugcPosts'
# Rest.li 2.0 structured query values: sent raw, only the URNs inside are encoded.
RESTLI_STRUCTURED = (INTERVALS, SHARE_IDS, UGC_IDS)
COUNT = 'count'
START = 'start'
SORT = 'sortBy'
CREATED_SORT = 'CREATED'
SHARE_ID = 'share'
UGC_ID = 'ugcPost'
ELEMENTS = 'elements'
PAGING = 'paging'
LINKS = 'links'
REL = 'rel'
NEXT = 'next'
INTERVAL = 'timeRange'
END = 'end'
PAGE_STATS = 'totalPageStatistics'
VIEWS = 'views'
ALL_VIEWS = 'allPageViews'
PAGE_VIEWS = 'pageViews'
SHARE_STATS = 'totalShareStatistics'
IMPRESSION = 'impressionCount'
CLICK = 'clickCount'
REACTION = 'likeCount'
COMMENT = 'commentCount'
SHARE = 'shareCount'
FOLLOWER_GAIN = 'followerGains'
ORGANIC_GAIN = 'organicFollowerGain'
PAID_GAIN = 'paidFollowerGain'
ID = 'id'
CREATED = 'createdAt'
TEXT = 'commentary'
ATTRIBUTES = 'attributes'
# Mention wire representations: aus Herstellerdoku, live zu belegen.
MENTION_MARKERS = ('urn:li:member:', 'urn:li:person:', 'memberattributedentity', 'mention', '@[')
LIFECYCLE = 'lifecycleState'
PUBLISHED = 'PUBLISHED'
CLIENT_ID = 'client_id'
CLIENT_SECRET = 'client_secret'
ACCESS_TOKEN = 'access_token'
EXPIRES_IN = 'expires_in'
REFRESH_TOKEN = 'refresh_token'
REFRESH_EXPIRES_IN = 'refresh_token_expires_in'
TOKEN_TYPE = 'token_type'
SCOPE = 'scope'
GRANT_TYPE = 'grant_type'
AUTH_CODE = 'authorization_code'
CODE = 'code'
STATE = 'state'
REDIRECT = 'redirect_uri'
RESPONSE_TYPE = 'response_type'
CODE_CHALLENGE = 'code_challenge'
CODE_CHALLENGE_METHOD = 'code_challenge_method'
CODE_VERIFIER = 'code_verifier'
S256 = 'S256'

# --- Stufe B1 (#1237): writing on the own organization page --------------------------------
# SEC c-6575359082 Bed. 1 and c-6575837332 V1-V3. Every name below: aus Herstellerdoku 202610,
# live zu belegen. Doc pages (Microsoft Learn, view li-lms-2026-10):
#   D_POSTS = https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/posts-api?view=li-lms-2026-10
#   D_IMAGES = .../shares/images-api?view=li-lms-2026-10 (upload: .../shares/vector-asset-api, "requires a valid OAuth token")
#   D_DOCS = .../shares/documents-api?view=li-lms-2026-10
#   D_VIDEOS = .../shares/videos-api?view=li-lms-2026-10 ("upload call shouldn't include an OAuth token")
#   D_MULTI = .../shares/multiimage-post-api?view=li-lms-2026-10 (2 to 20 images)
#   D_POLL = .../shares/poll-post-api?view=li-lms-2026-10 (question <=140, 2-4 options <=30, durations)
#   D_COMMENTS = .../shares/comments-api?view=li-lms-2026-10 (POST socialActions/{urn}/comments, actor org)
#   D_LITTLE = .../shares/little-text-format?view=li-lms-2026-10 (reserved characters, backslash escape)
# The write grant is its own consent and its own vault place (Bed. 4). Exactly two scopes:
#   w_organization_social: D_POSTS "Post, comment, and like posts on behalf of an organization"
#     (posts, edit, delete; images/documents/videos upload),
#   w_organization_social_feed: D_COMMENTS Permissions "Post, comment, and react on posts on behalf
#     of an organization" (create a comment as the organization).
# Gate-Befund VENDOR (#1237): the app already carries both. No further scope.
WRITE_SCOPES = ('w_organization_social', 'w_organization_social_feed')
# Closed list: operation -> (method, path below API_ROOT). Nothing else is ever sent to LinkedIn.
WRITE_ENDPOINTS = {
    'post_create': ('POST', 'posts'),                                   # D_POSTS 201, x-restli-id
    'post_update': ('POST', 'posts/{urn}'),                             # D_POSTS X-RestLi-Method PARTIAL_UPDATE, 204
    'post_delete': ('DELETE', 'posts/{urn}'),                           # D_POSTS X-RestLi-Method DELETE, 204 idempotent
    'comment_create': ('POST', 'socialActions/{urn}/comments'),         # D_COMMENTS, x-restli-id
    'image_init': ('POST', 'images?action=initializeUpload'),           # D_IMAGES value.uploadUrl/value.image
    'document_init': ('POST', 'documents?action=initializeUpload'),     # D_DOCS value.uploadUrl/value.document
    'video_init': ('POST', 'videos?action=initializeUpload'),           # D_VIDEOS value.uploadInstructions/video/uploadToken
    'video_finalize': ('POST', 'videos?action=finalizeUpload'),         # D_VIDEOS finalizeUploadRequest, 200
    'video_status': ('GET', 'videos/{urn}'),                            # D_VIDEOS status
}
RESTLI_METHOD = 'X-RestLi-Method'
RESTLI_METHODS = {'post_update': 'PARTIAL_UPDATE', 'post_delete': 'DELETE'}
RESTLI_ID = 'x-restli-id'
ETAG = 'etag'
# Upload URL comes from a LinkedIn answer (V1): exact host, https, no port/userinfo, fixed prefix.
# All three doc examples upload to https://www.linkedin.com/dms-uploads/...
UPLOAD_METHOD = 'PUT'
UPLOAD_HOSTS = ('www.linkedin.com',)
UPLOAD_PATH_PREFIX = '/dms-uploads/'
UPLOAD_TOKEN = {'bild': True, 'dokument': True, 'video': False}
VIDEO_PART_BYTES = 4 * 1024 * 1024        # D_VIDEOS: 0-4194303, 4194304-8388607, ...
VIDEO_AVAILABLE = 'AVAILABLE'
VIDEO_PENDING = ('PROCESSING', 'WAITING_UPLOAD')
VIDEO_POLL_SECONDS = 30
VIDEO_MAX_WAIT_SECONDS = 15 * 60
INIT_REQUEST = 'initializeUploadRequest'
FINALIZE_REQUEST = 'finalizeUploadRequest'
OWNER = 'owner'
FILE_SIZE = 'fileSizeBytes'
UPLOAD_CAPTIONS = 'uploadCaptions'
UPLOAD_THUMBNAIL = 'uploadThumbnail'
VALUE = 'value'
UPLOAD_URL = 'uploadUrl'
UPLOAD_INSTRUCTIONS = 'uploadInstructions'
UPLOAD_TOKEN_FIELD = 'uploadToken'
FIRST_BYTE = 'firstByte'
LAST_BYTE = 'lastByte'
UPLOADED_PARTS = 'uploadedPartIds'
IMAGE = 'image'
DOCUMENT = 'document'
VIDEO = 'video'
STATUS = 'status'
COMMENTARY = 'commentary'
POST_SHELL = {'visibility': 'PUBLIC',
              'distribution': {'feedDistribution': 'MAIN_FEED', 'targetEntities': [], 'thirdPartyDistributionChannels': []},
              'lifecycleState': 'PUBLISHED', 'isReshareDisabledByAuthor': False}
CONTENT = 'content'
MEDIA = 'media'
MULTI_IMAGE = 'multiImage'
IMAGES = 'images'
TITLE = 'title'
POLL = 'poll'
POLL_QUESTION = 'question'
POLL_OPTIONS = 'options'
POLL_TEXT = 'text'
POLL_SETTINGS = 'settings'
POLL_DURATION = 'duration'
POLL_DURATIONS = {1: 'ONE_DAY', 3: 'THREE_DAYS', 7: 'SEVEN_DAYS', 14: 'FOURTEEN_DAYS'}
POLL_QUESTION_MAX = 140
POLL_OPTION_MAX = 30
POLL_OPTIONS_MIN, POLL_OPTIONS_MAX = 2, 4
MULTI_IMAGE_MIN, MULTI_IMAGE_MAX = 2, 20
PATCH = 'patch'
SET = '$set'
ACTOR = 'actor'
OBJECT = 'object'
MESSAGE = 'message'
MESSAGE_TEXT = 'text'
LITTLE_RESERVED = '|{}@[]()<>#\\*_~'
