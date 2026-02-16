// Global submission state manager
const SubmissionState = {
    isSubmitting: false,

    start() {
        if (this.isSubmitting) {
            return false; // Already submitting
        }
        this.isSubmitting = true;
        return true;
    },

    end() {
        this.isSubmitting = false;
    },

    check() {
        return this.isSubmitting;
    }
};

// Prevent modal close during submission
const originalCloseModal = window.closeModal;
window.closeModal = function() {
    if (SubmissionState.check()) {
        console.warn('Cannot close modal during submission');
        return;
    }
    if (originalCloseModal) {
        originalCloseModal();
    }
};
