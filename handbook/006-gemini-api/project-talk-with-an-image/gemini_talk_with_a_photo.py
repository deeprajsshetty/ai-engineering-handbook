import io
import os

import streamlit as st
from google import genai
from dotenv import load_dotenv, find_dotenv
from PIL import Image


def ask_and_get_answer(client, prompt, img):
    # Send both the text prompt and the PIL image to the model.
    # The new SDK accepts a list of mixed content (text + images).
    response = client.models.generate_content(
        model='gemini-flash-latest',
        contents=[prompt, img]
    )
    return response.text


def st_image_to_pil(st_image):
    # Read the raw bytes from the Streamlit UploadedFile object.
    image_data = st_image.read()

    # Wrap the bytes in a BytesIO buffer so PIL can open it.
    return Image.open(io.BytesIO(image_data))


# Application entry point
if __name__ == '__main__':
    # Load the GOOGLE_API_KEY from the .env file.
    load_dotenv(find_dotenv(), override=True)

    # Create a single client instance that will be reused for all API calls.
    client = genai.Client(api_key=os.environ.get('GOOGLE_API_KEY'))

    # Display the Gemini logo (image must be in the same directory).
    st.image('gemini.png')
    st.subheader('Talking With an Image ✨')

    # File uploader widget — accepts common image formats.
    img = st.file_uploader('Select an Image: ', type=['jpg', 'jpeg', 'png', 'gif'])

    if img:
        st.image(img, caption='Talk with this image.')

        # Text area where the user types their question about the image.
        prompt = st.text_area('Ask a question about this image: ')

        if prompt:
            # Convert the Streamlit uploaded file to a PIL Image before sending to Gemini.
            pil_image = st_image_to_pil(img)

            with st.spinner('Running ...'):
                answer = ask_and_get_answer(client, prompt, pil_image)
                st.text_area('Gemini Answer: ', value=answer)

            st.divider()

            # Initialize the history key in session state on the first question.
            if 'history' not in st.session_state:
                st.session_state.history = ''

            # Prepend the latest Q&A so the most recent entry appears at the top.
            value = f'Q: {prompt} \n\n A: {answer}'
            st.session_state.history = f'{value} \n\n {"-" * 100} \n\n {st.session_state.history}'

            st.text_area(label='Chat History', value=st.session_state.history, height=400, key='history')

# Run the app: streamlit run ./gemini_talk_with_a_photo.py
